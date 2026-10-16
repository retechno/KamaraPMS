package posagent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Agent is the loop of an enrolled print agent: it holds the long poll for jobs, writes the bytes it is given to the printers, reports how each
// attempt ended and sends a heartbeat with the status of its printers.
type Agent struct {
	Client    *Client
	Writer    *Writer
	Log       *slog.Logger
	Version   string
	PollWait  int           // seconds of one long poll; 0 takes the server's
	Heartbeat time.Duration // between beats; 0 is 30 seconds
	// Backoff bounds the wait after a failure of the network or of the server; tests shorten it.
	BackoffMin time.Duration
	BackoffMax time.Duration

	mu       sync.Mutex
	printers []Printer
	pending  atomic.Int64
}

func (a *Agent) defaults() {
	if a.Heartbeat <= 0 {
		a.Heartbeat = 30 * time.Second
	}
	if a.BackoffMin <= 0 {
		a.BackoffMin = time.Second
	}
	if a.BackoffMax <= 0 {
		a.BackoffMax = 60 * time.Second
	}
	if a.Writer == nil {
		a.Writer = NewWriter()
	}
	if a.Log == nil {
		a.Log = slog.Default()
	}
}

func (a *Agent) setPrinters(p []Printer) {
	a.mu.Lock()
	a.printers = p
	a.mu.Unlock()
}

func (a *Agent) listPrinters() []Printer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Printer(nil), a.printers...)
}

// Run works until ctx ends or the server refuses the token. A broken network is not an end: it waits, with a growing pause, and tries again.
func (a *Agent) Run(ctx context.Context) error {
	a.defaults()
	// the configuration first; the loop does not start until the server has answered once with this token
	wait := a.BackoffMin
	for {
		cfg, err := a.Client.Config(ctx)
		if err == nil {
			a.setPrinters(cfg.Printers)
			if a.PollWait == 0 {
				a.PollWait = cfg.PollSeconds
			}
			a.Log.Info("connected", "device", cfg.Name, "printers", len(cfg.Printers))
			break
		}
		if errors.Is(err, ErrRevoked) {
			a.Log.Error("stopped: the token is not accepted", "error", err)
			return err
		}
		a.Log.Warn("the server cannot be reached", "error", err, "retry_in", wait.String())
		if !sleep(ctx, wait) {
			return nil
		}
		wait = min(wait*2, a.BackoffMax)
	}
	if a.PollWait <= 0 {
		a.PollWait = 25
	}
	beatCtx, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	revoked := make(chan error, 1)
	go a.beatLoop(beatCtx, revoked)

	wait = a.BackoffMin
	for ctx.Err() == nil {
		select {
		case err := <-revoked:
			return err
		default:
		}
		job, err := a.Client.NextJob(ctx, a.PollWait)
		switch {
		case err == nil:
			wait = a.BackoffMin
			if job != nil {
				a.work(ctx, job)
			}
		case errors.Is(err, ErrRevoked):
			a.Log.Error("stopped: the token is not accepted", "error", err)
			return err
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, ErrReadOnly):
			a.Log.Warn("the subscription is read-only: waiting", "retry_in", time.Minute.String())
			if !sleep(ctx, max(a.BackoffMin, minDur(time.Minute, a.BackoffMax))) {
				return nil
			}
		default:
			a.Log.Warn("polling failed", "error", err, "retry_in", wait.String())
			if !sleep(ctx, wait) {
				return nil
			}
			wait = min(wait*2, a.BackoffMax)
		}
	}
	return nil
}

func minDur(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// work prints one job and reports the end of the attempt. The bytes are the server's: the agent adds and removes nothing.
func (a *Agent) work(ctx context.Context, j *Job) {
	a.pending.Add(1)
	defer a.pending.Add(-1)
	log := a.Log.With("job", j.ID, "printer", j.PrinterID, "kind", j.Kind, "attempt", j.Attempt)
	res := Result{Status: "PRINTED", Attempt: j.Attempt, PrinterStatus: StatusOnline}
	if len(j.Payload) == 0 {
		res = Result{Status: "FAILED", ErrorCode: CodeBadPayload, Attempt: j.Attempt}
	} else if err := a.Writer.Write(ctx, j.ConnectionInfo, j.Payload); err != nil {
		code := CodeOf(err)
		res = Result{Status: "FAILED", ErrorCode: code, Attempt: j.Attempt}
		if code == CodeConnect {
			res.PrinterStatus = StatusOffline
		}
		log.Warn("the job was not printed", "code", code, "error", err)
	} else {
		log.Info("printed", "bytes", len(j.Payload))
	}
	// the result is told with a few tries: if it is lost the lease runs out and the job prints a second time, which costs paper
	wait := a.BackoffMin
	for range 6 {
		err := a.Client.Report(ctx, j.ID, res)
		if err == nil {
			return
		}
		var api *APIError
		if errors.As(err, &api) && api.Code == "JOB_LEASE_LOST" {
			log.Warn("the lease of the job was lost before the result was told", "code", api.Code)
			return
		}
		if errors.Is(err, ErrRevoked) || ctx.Err() != nil {
			return
		}
		log.Warn("the result could not be told", "error", err, "retry_in", wait.String())
		if !sleep(ctx, wait) {
			return
		}
		wait = min(wait*2, a.BackoffMax)
	}
}

func (a *Agent) beatLoop(ctx context.Context, revoked chan<- error) {
	t := time.NewTicker(a.Heartbeat)
	defer t.Stop()
	for {
		a.beat(ctx, revoked)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// beat probes every printer (at the same time, each with its own time limit) and sends the heartbeat. What the server answers is the list of
// printers the device serves now.
func (a *Agent) beat(ctx context.Context, revoked chan<- error) {
	ps := a.listPrinters()
	reports := make([]PrinterReport, len(ps))
	var wg sync.WaitGroup
	for i, p := range ps {
		wg.Go(func() {
			pctx, cancel := context.WithTimeout(ctx, 6*time.Second)
			defer cancel()
			reports[i] = PrinterReport{ID: p.ID, Status: a.Writer.Probe(pctx, p.ConnectionInfo)}
		})
	}
	wg.Wait()
	cfg, err := a.Client.Beat(ctx, Heartbeat{Printers: reports, QueueDepth: int(a.pending.Load()), AgentVersion: a.Version})
	switch {
	case err == nil:
		a.setPrinters(cfg.Printers)
	case errors.Is(err, ErrRevoked):
		select {
		case revoked <- err:
		default:
		}
	case ctx.Err() == nil:
		a.Log.Warn("heartbeat failed", "error", err)
	}
}

// sleep waits d or until ctx ends; it reports whether the wait was complete.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

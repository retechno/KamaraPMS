package posagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// The agent against a server that is only httptest and a printer that is only a TCP listener: no database (docs/architecture/34-pos.md 20).

type fakeServer struct {
	t       *testing.T
	srv     *httptest.Server
	mu      sync.Mutex
	jobs    []Job
	results []Result
	resIDs  []string
	beats   []Heartbeat
	polls   int
	// failFirst answers this many polls with 500 before it serves jobs
	failFirst int
	// status makes every route answer with it (401 or 403 read-only)
	status   int
	code     string
	printers []Printer
	token    string
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{t: t, token: "kpd_testtoken"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/device/config", f.guard(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ServerConfig{DeviceID: 7, Name: "Kasir", PropertyID: 1, PollSeconds: 1, Printers: f.printers})
	}))
	mux.HandleFunc("GET /api/v1/device/jobs/next", f.guard(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.polls++
		if f.failFirst > 0 {
			f.failFirst--
			f.mu.Unlock()
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		var j *Job
		if len(f.jobs) > 0 {
			j, f.jobs = &f.jobs[0], f.jobs[1:]
		}
		f.mu.Unlock()
		if j == nil {
			select {
			case <-time.After(50 * time.Millisecond):
			case <-r.Context().Done():
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_ = json.NewEncoder(w).Encode(j)
	}))
	mux.HandleFunc("POST /api/v1/device/jobs/{id}/result", f.guard(func(w http.ResponseWriter, r *http.Request) {
		var res Result
		_ = json.NewDecoder(r.Body).Decode(&res)
		f.mu.Lock()
		f.results = append(f.results, res)
		f.resIDs = append(f.resIDs, r.PathValue("id"))
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"status":"PRINTED"}`)
	}))
	mux.HandleFunc("POST /api/v1/device/heartbeat", f.guard(func(w http.ResponseWriter, r *http.Request) {
		var h Heartbeat
		_ = json.NewDecoder(r.Body).Decode(&h)
		f.mu.Lock()
		f.beats = append(f.beats, h)
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(ServerConfig{DeviceID: 7, Printers: f.printers})
	}))
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeServer) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		status, code := f.status, f.code
		f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"DEVICE_TOKEN_INVALID"}`)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"code":"`+code+`"}`)
			return
		}
		h(w, r)
	}
}

func (f *fakeServer) resultsLen() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.results)
}

// fakePrinter listens on a port and keeps what it was sent. answer, when it is set, is what it says to a status request (DLE EOT).
type fakePrinter struct {
	ln     net.Listener
	mu     sync.Mutex
	got    [][]byte
	answer byte
}

func newFakePrinter(t *testing.T) *fakePrinter {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakePrinter{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
				var all []byte
				buf := make([]byte, 4096)
				for {
					n, err := c.Read(buf)
					all = append(all, buf[:n]...)
					if n > 0 && bytes.HasPrefix(buf[:n], []byte{0x10, 0x04}) && p.answer != 0 {
						_, _ = c.Write([]byte{p.answer})
						all = all[:0]
						continue
					}
					if err != nil {
						break
					}
				}
				if len(all) > 0 {
					p.mu.Lock()
					p.got = append(p.got, all)
					p.mu.Unlock()
				}
			}()
		}
	}()
	return p
}

func (p *fakePrinter) addr() string { return p.ln.Addr().String() }

func (p *fakePrinter) received() [][]byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][]byte(nil), p.got...)
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newAgent(f *fakeServer) *Agent {
	return &Agent{Client: NewClient(f.srv.URL, f.token), Writer: &Writer{DialTimeout: time.Second, WriteTimeout: time.Second}, Log: quiet(), Version: "test",
		PollWait: 1, Heartbeat: 100 * time.Millisecond, BackoffMin: 10 * time.Millisecond, BackoffMax: 50 * time.Millisecond}
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 300 {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", what)
}

func runAgent(t *testing.T, a *Agent) (stop func(), done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- a.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, ch
}

func TestTheAgentWritesTheBytesAsTheyAreAndReportsPrintedWithItsHeartbeat(t *testing.T) {
	f := newFakeServer(t)
	pr := newFakePrinter(t)
	f.printers = []Printer{{ID: 3, Name: "Kitchen", Kind: "KITCHEN", ConnectionInfo: pr.addr(), PaperWidthMM: 80}}
	payload := []byte{0x1B, 0x40, 'H', 'i', '\n', 0x1D, 0x56, 0x42, 0x00}
	f.jobs = []Job{{ID: "j1", PrinterID: 3, Kind: "TICKET", Attempt: 1, MaxAttempts: 3, Payload: payload, ConnectionInfo: pr.addr()}}
	stop, done := runAgent(t, newAgent(f))
	eventually(t, "the result", func() bool { return f.resultsLen() == 1 })
	got := pr.received()
	if len(got) != 1 || !bytes.Equal(got[0], payload) {
		t.Fatalf("the printer got %x, want %x: the agent adds and removes nothing", got, payload)
	}
	f.mu.Lock()
	res := f.results[0]
	f.mu.Unlock()
	if res.Status != "PRINTED" || res.Attempt != 1 || res.PrinterStatus != "ONLINE" || f.resIDs[0] != "j1" {
		t.Fatalf("result: %+v", res)
	}
	eventually(t, "a heartbeat that names the printer", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, b := range f.beats {
			if len(b.Printers) == 1 && b.Printers[0].ID == 3 && b.Printers[0].Status == "ONLINE" && b.AgentVersion == "test" {
				return true
			}
		}
		return false
	})
	stop()
	if err := <-done; err != nil {
		t.Fatalf("a stop is not an error: %v", err)
	}
}

func TestAPrinterThatCannotBeReachedFailsTheAttemptWithItsCode(t *testing.T) {
	f := newFakeServer(t)
	dead := newFakePrinter(t)
	addr := dead.addr()
	_ = dead.ln.Close() // nothing listens there now
	f.printers = []Printer{{ID: 3, Name: "Kitchen", ConnectionInfo: addr}}
	f.jobs = []Job{{ID: "j1", PrinterID: 3, Kind: "TICKET", Attempt: 2, Payload: []byte("x"), ConnectionInfo: addr}}
	runAgent(t, newAgent(f))
	eventually(t, "the result", func() bool { return f.resultsLen() == 1 })
	f.mu.Lock()
	res := f.results[0]
	f.mu.Unlock()
	if res.Status != "FAILED" || res.ErrorCode != CodeConnect || res.PrinterStatus != "OFFLINE" || res.Attempt != 2 {
		t.Fatalf("result: %+v", res)
	}
}

func TestAnUnsupportedConnectionAndAnEmptyPayloadFailWithTheirOwnCodes(t *testing.T) {
	f := newFakeServer(t)
	f.jobs = []Job{
		{ID: "a", PrinterID: 1, Attempt: 1, Payload: []byte("x"), ConnectionInfo: "USB:Epson"},
		{ID: "b", PrinterID: 1, Attempt: 1, Payload: nil, ConnectionInfo: "127.0.0.1:9100"},
	}
	runAgent(t, newAgent(f))
	eventually(t, "two results", func() bool { return f.resultsLen() == 2 })
	f.mu.Lock()
	defer f.mu.Unlock()
	want := CodeUnsupported
	if runtime.GOOS == "windows" {
		want = CodeConnect // the Windows spooler is there and has no such printer
	}
	if f.results[0].ErrorCode != want || f.results[1].ErrorCode != CodeBadPayload {
		t.Fatalf("results: %+v", f.results)
	}
}

func TestARevokedTokenStopsTheAgentAtOnce(t *testing.T) {
	f := newFakeServer(t)
	a := newAgent(f)
	a.Client.Token = "kpd_revoked"
	_, done := runAgent(t, a)
	select {
	case err := <-done:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("err: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an agent whose token is not accepted must stop, not retry")
	}
}

func TestTheAgentRecoversFromANetworkThatBreaksAndKeepsPollingThroughReadOnly(t *testing.T) {
	f := newFakeServer(t)
	pr := newFakePrinter(t)
	f.printers = []Printer{{ID: 3, Name: "Kitchen", ConnectionInfo: pr.addr()}}
	f.failFirst = 3
	f.jobs = []Job{{ID: "j1", PrinterID: 3, Attempt: 1, Payload: []byte("ok"), ConnectionInfo: pr.addr()}}
	runAgent(t, newAgent(f))
	eventually(t, "the job after three errors of the server", func() bool { return f.resultsLen() == 1 })
	// a server that says read-only is waited out, not a reason to stop
	f2 := newFakeServer(t)
	f2.status, f2.code = http.StatusForbidden, "SUBSCRIPTION_READ_ONLY"
	a := newAgent(f2)
	_, done := runAgent(t, a)
	_ = done
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("read-only does not stop the agent: %v", err)
	default:
	}
}

func TestAPrinterThatSaysPaperOutOrCoverOpenIsReportedSo(t *testing.T) {
	w := &Writer{DialTimeout: time.Second, WriteTimeout: time.Second}
	pr := newFakePrinter(t)
	pr.answer = 0x60
	if got := w.Probe(context.Background(), pr.addr()); got != StatusPaperOut {
		t.Fatalf("paper: %s", got)
	}
	pr.answer = 0x04 // paper is fine for DLE EOT 4 (bit 0x60 clear) and bit 2 says the cover is open for DLE EOT 2
	if got := w.Probe(context.Background(), pr.addr()); got != StatusCoverOpen {
		t.Fatalf("cover: %s", got)
	}
	pr.answer = 0
	if got := w.Probe(context.Background(), pr.addr()); got != StatusOnline {
		t.Fatalf("silent printer: %s", got)
	}
	if got := w.Probe(context.Background(), "127.0.0.1:1"); got != StatusOffline {
		t.Fatalf("unreachable: %s", got)
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		want Target
		bad  bool
	}{
		{"192.168.1.50:9100", Target{"TCP", "192.168.1.50:9100"}, false},
		{"192.168.1.50", Target{"TCP", "192.168.1.50:9100"}, false},
		{"usb:EPSON TM-T82", Target{"USB", "EPSON TM-T82"}, false},
		{"DEV:/dev/usb/lp0", Target{"DEV", "/dev/usb/lp0"}, false},
		{"", Target{}, true},
		{"USB:", Target{}, true},
		{"DEV:", Target{}, true},
		{"a:b:c", Target{}, true},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.in)
		if (err != nil) != c.bad || got != c.want {
			t.Errorf("%q: %+v %v", c.in, got, err)
		}
	}
}

func TestTheConfigIsKeptPrivateAndTheLogRotates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pos-agent", "config.json")
	cfg := FileConfig{Server: "https://pms.example.com", Token: "kpd_secret", Name: "Kasir", DeviceID: 4}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got != cfg {
		t.Fatalf("load: %+v %v", got, err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("the file is %v: the token is for its owner only", st.Mode().Perm())
		}
		ds, _ := os.Stat(filepath.Dir(path))
		if ds.Mode().Perm() != 0o700 {
			t.Fatalf("the directory is %v", ds.Mode().Perm())
		}
	}
	if _, err := LoadConfig(filepath.Join(dir, "none.json")); err == nil || !strings.Contains(err.Error(), "enroll") {
		t.Fatalf("a missing configuration says what to do: %v", err)
	}
	// the log keeps three old files and never grows without limit
	lp := filepath.Join(dir, "agent.log")
	r, err := NewRotatingFile(lp, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		_, _ = r.Write([]byte(strings.Repeat("x", 40) + "\n"))
	}
	_ = r.Close()
	for _, p := range []string{lp, lp + ".1", lp + ".2", lp + ".3"} {
		st, err := os.Stat(p)
		if err != nil || st.Size() > 100 {
			t.Fatalf("%s: %v %v", p, st, err)
		}
	}
	if _, err := os.Stat(lp + ".4"); err == nil {
		t.Fatal("only three old files are kept")
	}
}

func TestTheTokenIsNeverInALogLine(t *testing.T) {
	f := newFakeServer(t)
	var buf bytes.Buffer
	a := newAgent(f)
	a.Log = slog.New(slog.NewTextHandler(&buf, nil))
	a.Client.Token = "kpd_wrongwrongwrong"
	_, done := runAgent(t, a)
	<-done
	if strings.Contains(buf.String(), "wrongwrongwrong") {
		t.Fatalf("the token is in the log: %s", buf.String())
	}
}

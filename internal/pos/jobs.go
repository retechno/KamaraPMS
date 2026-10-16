package pos

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/pos/posdb"
	"kamarapms/internal/pos/printing"
)

// The claim and the result of a print job, from the side of the agent (docs/architecture/34-pos.md 10.3 and 20).

const (
	// LeaseDuration is how long an agent holds a job before it returns to the queue.
	LeaseDuration = 60 * time.Second
	// MaxWait is the longest long poll the server holds.
	MaxWait = 30 * time.Second
	// PurgeEvery is how often the claim path purges the payloads that are past their retention.
	PurgeEvery = time.Hour
)

// ClaimedJob is a job as the agent receives it: the bytes to write, where to write them, and which attempt this is.
type ClaimedJob struct {
	ID             string    `json:"id"`
	PrinterID      int64     `json:"printer_id"`
	Kind           string    `json:"kind"`
	CopyNo         int       `json:"copy_no"`
	Attempt        int       `json:"attempt"`
	MaxAttempts    int       `json:"max_attempts"`
	Payload        []byte    `json:"payload"` // base64 in JSON
	ConnectionInfo string    `json:"connection_info"`
	LeaseUntil     time.Time `json:"lease_until"`
}

// NextJob waits up to wait for a job of one of the printers of the device and claims it. Jobs of one printer come in order: a job waits for the
// one before it. It answers nil when the wait ends with nothing to print.
func (s *Service) NextJob(ctx context.Context, dev DeviceIdentity, wait time.Duration) (*ClaimedJob, error) {
	if wait > MaxWait {
		wait = MaxWait
	}
	s.maybePurgePayloads(ctx)
	done, err := s.hub.Open(-dev.ID) // the wake-up of the streams is the one of the claims; a device counts as a user
	if err != nil {
		return nil, err
	}
	defer done()
	deadline := s.clock.Now().Add(wait)
	for first := true; ; first = false {
		wake := s.hub.Wait()
		job, err := s.claimOnce(ctx, dev, first)
		if err != nil || job != nil {
			return job, err
		}
		left := deadline.Sub(s.clock.Now())
		if left <= 0 {
			return nil, nil
		}
		timer := time.NewTimer(min(left, 3*time.Second)) // a missed wake-up delays the claim by seconds and loses nothing
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, nil
		case <-wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// claimOnce is one try: expired leases go back, then the first printer with a job that is due gets it.
func (s *Service) claimOnce(ctx context.Context, dev DeviceIdentity, touch bool) (*ClaimedJob, error) {
	var out *ClaimedJob
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		if touch { // the device was seen: once for each poll, not for each look of a long one
			if err := s.touchDevice(ctx, dev, "", nil); err != nil {
				return err
			}
		}
		if err := q.RequeueExpiredLeases(ctx, posdb.RequeueExpiredLeasesParams{DeviceID: &dev.ID, PropertyID: dev.PropertyID, Now: now}); err != nil {
			return err
		}
		cands, err := q.ClaimCandidates(ctx, posdb.ClaimCandidatesParams{PropertyID: dev.PropertyID, DeviceID: &dev.ID})
		if err != nil {
			return err
		}
		for _, c := range cands {
			if c.Status != "QUEUED" || (c.NextAttemptAt != nil && c.NextAttemptAt.After(now)) {
				continue // the printer is busy with a job (or waits for its backoff): its later jobs wait
			}
			row, err := q.ClaimJob(ctx, posdb.ClaimJobParams{ID: c.ID, PropertyID: dev.PropertyID, DeviceID: &dev.ID, LeaseUntil: ptr(now.Add(LeaseDuration)), Now: now})
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) { // another poll of the same agent took it between the read and the claim
					continue
				}
				return err
			}
			payload := row.Payload
			if len(payload) == 0 {
				var perr error
				if payload, perr = s.renderLate(ctx, dev, row); perr != nil {
					// nothing to print for this job: it fails for good with its own code, and the next job of the printer is tried
					if err := q.JobFailed(ctx, posdb.JobFailedParams{ID: row.ID, ErrorCode: ptr(errCode(perr))}); err != nil {
						return err
					}
					continue
				}
			}
			pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: dev.PropertyID, ID: row.PrinterID})
			if err != nil {
				return err
			}
			out = &ClaimedJob{ID: uuidString(row.ID), PrinterID: row.PrinterID, Kind: row.Kind, CopyNo: int(row.CopyNo), Attempt: int(row.Attempts), MaxAttempts: int(row.MaxAttempts),
				Payload: payload, ConnectionInfo: pr.ConnectionInfo, LeaseUntil: now.Add(LeaseDuration)}
			return nil
		}
		return nil
	})
	return out, err
}

func errCode(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return "RENDER_FAILED"
}

// renderLate makes the bytes of a job that was queued without them (a ticket queued by stage 3a, before documents were stored). A job whose
// document was purged cannot be made again.
func (s *Service) renderLate(ctx context.Context, dev DeviceIdentity, row posdb.ClaimJobRow) ([]byte, error) {
	if row.PayloadPurgedAt != nil {
		return nil, apperr.Conflict("PAYLOAD_PURGED", "the document of this job was removed")
	}
	var doc printing.Document
	switch {
	case len(row.Document) > 0:
		if err := json.Unmarshal(row.Document, &doc); err != nil {
			return nil, err
		}
	case row.SourceType == "TICKET":
		d, err := s.ticketDocument(ctx, row.TenantID, dev.PropertyID, row.SourceID)
		if err != nil {
			return nil, err
		}
		doc = d
	case row.Kind == printing.KindDrawer:
		doc = printing.Drawer()
	default:
		return nil, apperr.Conflict("NO_DOCUMENT", "the job has no document")
	}
	pr, err := s.q(ctx).PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: dev.PropertyID, ID: row.PrinterID})
	if err != nil {
		return nil, err
	}
	mark := ""
	if row.ReprintOf.Valid {
		mark = "COPY " + itoa(int(row.CopyNo))
	}
	b, err := printing.Render(doc, printing.Profile{WidthMM: int(pr.PaperWidthMm), Codepage: pr.Codepage}, mark)
	if err != nil {
		return nil, err
	}
	docJSON, _ := json.Marshal(doc)
	if err := s.q(ctx).SetJobPayload(ctx, posdb.SetJobPayloadParams{ID: row.ID, PropertyID: dev.PropertyID, Document: docJSON, Payload: b}); err != nil {
		return nil, err
	}
	return b, nil
}

// JobResult is what an agent says about a job it claimed.
type JobResult struct {
	Status        string `json:"status"` // PRINTED or FAILED
	ErrorCode     string `json:"error_code"`
	PrinterStatus string `json:"printer_status"`
	Attempt       int    `json:"attempt"`
}

// ResultAnswer is the state of the job after the result, the same for a repeated report.
type ResultAnswer struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Attempts int    `json:"attempts"`
	Final    bool   `json:"final"`
}

var backoff = []time.Duration{10 * time.Second, 60 * time.Second}

// ReportResult records the end of an attempt. It is idempotent by job and attempt: the same report again answers the same state and changes
// nothing. A report for an attempt whose lease was lost (the job went to another attempt) is a conflict the agent can ignore.
//
//	PRINTED                      → PRINTED
//	FAILED, attempts < max       → QUEUED again after 10 s, then 60 s
//	FAILED, attempts = max       → FAILED for good, an event, and an audit entry
func (s *Service) ReportResult(ctx context.Context, dev DeviceIdentity, jobID string, in JobResult) (ResultAnswer, error) {
	if in.Status != "PRINTED" && in.Status != "FAILED" {
		return ResultAnswer{}, invalid("result", fieldErr("status", "INVALID_VALUE", "PRINTED or FAILED"))
	}
	if in.Attempt < 1 {
		return ResultAnswer{}, invalid("result", fieldErr("attempt", "REQUIRED", "the attempt of the job you were given"))
	}
	if in.PrinterStatus != "" && !printerStatuses[in.PrinterStatus] {
		return ResultAnswer{}, invalid("result", fieldErr("printer_status", "INVALID_VALUE", "ONLINE, OFFLINE, PAPER_OUT or COVER_OPEN"))
	}
	code := truncate(in.ErrorCode, 40)
	if in.Status == "FAILED" && code == "" {
		code = "PRINT_FAILED"
	}
	u, fe := parseUUID("id", jobID)
	if fe != nil {
		return ResultAnswer{}, notFound("PRINT_JOB_NOT_FOUND", "print job")
	}
	var out ResultAnswer
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		j, err := q.JobForResult(ctx, posdb.JobForResultParams{ID: u, PropertyID: dev.PropertyID})
		if err != nil {
			return orNotFound(err, notFound("PRINT_JOB_NOT_FOUND", "print job"))
		}
		pr, err := q.PrinterForJob(ctx, posdb.PrinterForJobParams{PropertyID: dev.PropertyID, ID: j.PrinterID})
		if err != nil || pr.DeviceID == nil || *pr.DeviceID != dev.ID { // a device sees the jobs of its own printers only
			return notFound("PRINT_JOB_NOT_FOUND", "print job")
		}
		answer := func() ResultAnswer {
			return ResultAnswer{ID: jobID, Status: j.Status, Attempts: int(j.Attempts), Final: j.Status == "PRINTED" || j.Status == "FAILED" || j.Status == "CANCELLED"}
		}
		if in.PrinterStatus != "" {
			st := in.PrinterStatus
			if _, err := q.ReportPrinterStatus(ctx, posdb.ReportPrinterStatusParams{PropertyID: dev.PropertyID, ID: j.PrinterID, DeviceID: &dev.ID, Status: &st, Now: now}); err != nil {
				return err
			}
		}
		// the report of an attempt that was already reported, or of one that is not the current attempt
		if j.Status != "CLAIMED" || j.ClaimedBy == nil || *j.ClaimedBy != dev.ID || int(j.Attempts) != in.Attempt {
			if int(j.Attempts) >= in.Attempt && j.Status != "CLAIMED" {
				out = answer() // reported before: the state it has now
				return nil
			}
			return apperr.Conflict("JOB_LEASE_LOST", "this job is not held by the device for that attempt").WithContext("status", j.Status).WithContext("attempts", int(j.Attempts))
		}
		switch {
		case in.Status == "PRINTED":
			if err := q.JobPrinted(ctx, posdb.JobPrintedParams{ID: u, Now: now}); err != nil {
				return err
			}
			j.Status = "PRINTED"
		case int(j.Attempts) < int(j.MaxAttempts):
			next := now.Add(backoff[min(int(j.Attempts)-1, len(backoff)-1)])
			if err := q.JobRequeue(ctx, posdb.JobRequeueParams{ID: u, ErrorCode: &code, NextAttemptAt: &next}); err != nil {
				return err
			}
			j.Status = "QUEUED"
		default:
			if err := q.JobFailed(ctx, posdb.JobFailedParams{ID: u, ErrorCode: &code}); err != nil {
				return err
			}
			j.Status = "FAILED"
			pid, printer := dev.PropertyID, j.PrinterID
			if err := s.audit.Write(ctx, audit.Entry{TenantID: dev.TenantID, PropertyID: &pid, Action: "pos.print_failed", EntityType: "pos_printer", EntityID: printer,
				EntityLabel: j.Kind, New: map[string]any{"job": jobID, "kind": j.Kind, "error_code": code, "attempts": int(j.Attempts), "source_type": j.SourceType, "source_id": j.SourceID}}); err != nil {
				return err
			}
		}
		out = answer()
		return nil
	})
	return out, err
}

// maybePurgePayloads removes the documents and the bytes past their retention, at most once an hour by process, from the first claim after.
// The agents poll all day, so this runs without a scheduler of its own (docs/architecture/34-pos.md 20).
func (s *Service) maybePurgePayloads(ctx context.Context) {
	now := s.clock.Now()
	s.purgeMu.Lock()
	due := now.Sub(s.payloadsPurgedAt) > PurgeEvery
	if due {
		s.payloadsPurgedAt = now
	}
	s.purgeMu.Unlock()
	if due {
		_, _ = s.PurgePrintPayloads(ctx)
	}
}

// PurgePrintPayloads fails the jobs that never printed within the retention and removes the document and the bytes of the jobs older than it. The
// rows stay: what was printed, for whom, when and how it ended.
func (s *Service) PurgePrintPayloads(ctx context.Context) (int64, error) {
	var n int64
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		cutoff := now.Add(-PayloadRetention)
		if err := q.ExpireOldJobs(ctx, cutoff); err != nil {
			return err
		}
		var err error
		n, err = q.PurgePayloads(ctx, posdb.PurgePayloadsParams{Now: now, Cutoff: cutoff})
		return err
	})
	return n, err
}

// Package posagent is the print agent of a hotel: it calls the server over HTTPS, takes the print jobs of its printers and writes the bytes it
// is given to the printer, as they are (docs/architecture/34-pos.md 10.3 and 20). It has no database and decides nothing about the paper.
package posagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Errors an agent acts on.
var (
	// ErrRevoked is a 401 from the server: the token is unknown or revoked. The agent stops and says so; it does not retry.
	ErrRevoked = errors.New("the server does not accept this device token: it was revoked or replaced; enrol the agent again")
	// ErrReadOnly is the subscription of the hotel in read-only state: the agent keeps its jobs and tries again.
	ErrReadOnly = errors.New("the subscription is read-only: the server refuses the claim for now")
)

// APIError is an answer of the server that is not a success.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("server answered %d %s: %s", e.Status, e.Code, e.Message)
}

// Printer is a printer as the server describes it to the agent.
type Printer struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ConnectionInfo string `json:"connection_info"`
	PaperWidthMM   int    `json:"paper_width_mm"`
	OpensDrawer    bool   `json:"opens_drawer"`
}

// Config is what the server says about this device.
type ServerConfig struct {
	DeviceID    int64     `json:"device_id"`
	Name        string    `json:"name"`
	PropertyID  int64     `json:"property_id"`
	PollSeconds int       `json:"poll_seconds"`
	Printers    []Printer `json:"printers"`
}

// Job is a claimed job.
type Job struct {
	ID             string    `json:"id"`
	PrinterID      int64     `json:"printer_id"`
	Kind           string    `json:"kind"`
	CopyNo         int       `json:"copy_no"`
	Attempt        int       `json:"attempt"`
	MaxAttempts    int       `json:"max_attempts"`
	Payload        []byte    `json:"payload"`
	ConnectionInfo string    `json:"connection_info"`
	LeaseUntil     time.Time `json:"lease_until"`
}

// Result is what the agent reports about a job.
type Result struct {
	Status        string `json:"status"`
	ErrorCode     string `json:"error_code,omitempty"`
	PrinterStatus string `json:"printer_status,omitempty"`
	Attempt       int    `json:"attempt"`
}

// PrinterReport is the status of one printer in a heartbeat.
type PrinterReport struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// Heartbeat is the beat of the agent: how its printers are and how many jobs it holds.
type Heartbeat struct {
	Printers     []PrinterReport `json:"printers"`
	QueueDepth   int             `json:"queue_depth"`
	AgentVersion string          `json:"agent_version"`
}

// Client calls the device routes. The token is only ever put in the Authorization header: it is not in a URL and not in a log line.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient makes a client with a timeout for ordinary calls (a long poll lengthens it itself).
func NewClient(base, token string) *Client {
	return &Client{BaseURL: strings.TrimRight(base, "/"), Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) do(ctx context.Context, method, path string, body, out any, timeout time.Duration) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, scrub(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	switch {
	case res.StatusCode == http.StatusNoContent:
		return res.StatusCode, nil
	case res.StatusCode >= 200 && res.StatusCode < 300:
		if out != nil {
			if err := json.Unmarshal(raw, out); err != nil {
				return res.StatusCode, fmt.Errorf("the answer of the server is not what the agent expects: %w", err)
			}
		}
		return res.StatusCode, nil
	}
	var p struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	_ = json.Unmarshal(raw, &p)
	msg := p.Detail
	if msg == "" {
		msg = p.Title
	}
	switch {
	case res.StatusCode == http.StatusUnauthorized:
		return res.StatusCode, fmt.Errorf("%w (%s)", ErrRevoked, p.Code)
	case res.StatusCode == http.StatusForbidden && p.Code == "SUBSCRIPTION_READ_ONLY":
		return res.StatusCode, ErrReadOnly
	}
	return res.StatusCode, &APIError{Status: res.StatusCode, Code: p.Code, Message: msg}
}

// scrub keeps the URL query out of a network error: the token is never in it, but a path may be.
func scrub(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}
	return err
}

// Enroll turns a code into a token.
func (c *Client) Enroll(ctx context.Context, code, name, version string) (token string, cfg ServerConfig, err error) {
	var out struct {
		DeviceID    int64  `json:"device_id"`
		Name        string `json:"name"`
		Token       string `json:"token"`
		PollSeconds int    `json:"poll_seconds"`
		PropertyID  int64  `json:"property_id"`
	}
	_, err = c.do(ctx, http.MethodPost, "/api/v1/device/enroll", map[string]string{"code": code, "name": name, "agent_version": version}, &out, 0)
	if err != nil {
		return "", ServerConfig{}, err
	}
	return out.Token, ServerConfig{DeviceID: out.DeviceID, Name: out.Name, PropertyID: out.PropertyID, PollSeconds: out.PollSeconds}, nil
}

// Config reads the printers of this device.
func (c *Client) Config(ctx context.Context) (ServerConfig, error) {
	var out ServerConfig
	_, err := c.do(ctx, http.MethodGet, "/api/v1/device/config", nil, &out, 0)
	return out, err
}

// NextJob waits up to waitSeconds for a job. It returns nil, nil when there is none.
func (c *Client) NextJob(ctx context.Context, waitSeconds int) (*Job, error) {
	var out Job
	status, err := c.do(ctx, http.MethodGet, "/api/v1/device/jobs/next?wait="+strconv.Itoa(waitSeconds), nil, &out, time.Duration(waitSeconds+15)*time.Second)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	return &out, nil
}

// Report says how an attempt ended.
func (c *Client) Report(ctx context.Context, jobID string, r Result) error {
	_, err := c.do(ctx, http.MethodPost, "/api/v1/device/jobs/"+url.PathEscape(jobID)+"/result", r, nil, 0)
	return err
}

// Beat sends a heartbeat and receives the printers the server now assigns to this device.
func (c *Client) Beat(ctx context.Context, h Heartbeat) (ServerConfig, error) {
	var out ServerConfig
	_, err := c.do(ctx, http.MethodPost, "/api/v1/device/heartbeat", h, &out, 0)
	return out, err
}

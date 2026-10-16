package pos

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/pos/posdb"
)

// The print agents of a hotel (docs/architecture/34-pos.md 10.3 and 20): a device is enrolled with a one-time code, holds one token (stored as
// SHA-256) and serves the printers assigned to it. The server never calls the hotel: the agent calls out.

const (
	// EnrollmentTTL is how long an enrolment code lives.
	EnrollmentTTL = 15 * time.Minute
	// OfflineAfter is how long a silent device or printer is shown as offline.
	OfflineAfter = 90 * time.Second
	// DeviceTokenPrefix starts every device token, so a token of a user (a JWT) and a token of a device are never confused.
	DeviceTokenPrefix = "kpd_"
	// PollSeconds is the long poll the server asks of an agent.
	PollSeconds = 25
)

// Device is an agent of the property.
type Device struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Kind         string     `json:"kind"`
	TokenPrefix  string     `json:"token_prefix"`
	Status       string     `json:"status"`
	LastSeenAt   *time.Time `json:"last_seen_at"`
	AgentVersion *string    `json:"agent_version"`
	QueueDepth   int        `json:"queue_depth"`
	CreatedAt    time.Time  `json:"created_at"`
	RotatedAt    *time.Time `json:"rotated_at"`
	RevokedAt    *time.Time `json:"revoked_at"`
	Printers     int        `json:"printers"`
	// Online is derived: active, and seen within OfflineAfter.
	Online bool `json:"online"`
}

// DeviceList is the devices of a property and the enrolment codes that are still open.
type DeviceList struct {
	Data               []Device   `json:"data"`
	PendingEnrollments int        `json:"pending_enrollments"`
	EnrollmentExpires  *time.Time `json:"enrollment_expires_at"`
}

// Enrollment is a new one-time code: shown once.
type Enrollment struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	Seconds   int       `json:"expires_in_seconds"`
}

// DeviceToken is what a new or rotated token looks like to the caller: the token is shown once, only its hash is kept.
type DeviceToken struct {
	Device Device `json:"device"`
	Token  string `json:"token"`
}

// EnrolledDevice answers the agent that enrolled.
type EnrolledDevice struct {
	DeviceID    int64  `json:"device_id"`
	Name        string `json:"name"`
	Token       string `json:"token"`
	PollSeconds int    `json:"poll_seconds"`
	PropertyID  int64  `json:"property_id"`
}

// DeviceIdentity is who a device token says it is.
type DeviceIdentity struct {
	ID         int64
	TenantID   int64
	PropertyID int64
	Name       string
}

// DevicePrinter is a printer as the agent needs to know it.
type DevicePrinter struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ConnectionInfo string `json:"connection_info"`
	PaperWidthMM   int    `json:"paper_width_mm"`
	OpensDrawer    bool   `json:"opens_drawer"`
}

// DeviceConfig is what the agent asks at its start and after each heartbeat.
type DeviceConfig struct {
	DeviceID    int64           `json:"device_id"`
	Name        string          `json:"name"`
	PropertyID  int64           `json:"property_id"`
	PollSeconds int             `json:"poll_seconds"`
	ServerTime  time.Time       `json:"server_time"`
	Printers    []DevicePrinter `json:"printers"`
}

// PrinterState says what a printer is now, from what its agent reported and when: NO_DEVICE (no agent is assigned), UNKNOWN (never reported),
// OFFLINE (silent for more than OfflineAfter, or reported so) or the status it reported.
func PrinterState(hasDevice bool, last *string, at *time.Time, now time.Time) string {
	return printerState(hasDevice, false, last, at, now)
}

// printerState is PrinterState with the state of the device: a revoked device serves nothing, so its printers are offline whatever they last said.
func printerState(hasDevice, revoked bool, last *string, at *time.Time, now time.Time) string {
	switch {
	case !hasDevice:
		return "NO_DEVICE"
	case revoked:
		return "OFFLINE"
	case last == nil || at == nil:
		return "UNKNOWN"
	case now.Sub(*at) > OfflineAfter:
		return "OFFLINE"
	}
	return *last
}

var codeAlphabet = base32.NewEncoding("ABCDEFGHJKLMNPQRSTUVWXYZ23456789").WithPadding(base32.NoPadding)

func hashOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// normalCodeOfEnrollment makes "k7qm 4txd" and "K7QM-4TXD" the same code.
func normalEnrollmentCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

func newEnrollmentCode() (string, error) {
	b := make([]byte, 5) // 40 bits: eight characters of a 32-letter alphabet
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	c := codeAlphabet.EncodeToString(b)
	return c[:4] + "-" + c[4:], nil
}

func newDeviceToken() (token, prefix, hash string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return "", "", "", err
	}
	token = DeviceTokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, token[:12], hashOf(token), nil
}

func (s *Service) toDevice(r posdb.ListDevicesRow, now time.Time) Device {
	d := Device{ID: r.ID, Name: r.Name, Kind: r.Kind, TokenPrefix: r.TokenPrefix, Status: r.Status, LastSeenAt: r.LastSeenAt, AgentVersion: r.AgentVersion, QueueDepth: int(r.QueueDepth),
		CreatedAt: r.CreatedAt, RotatedAt: r.RotatedAt, RevokedAt: r.RevokedAt, Printers: int(r.Printers)}
	d.Online = r.Status == "ACTIVE" && r.LastSeenAt != nil && now.Sub(*r.LastSeenAt) <= OfflineAfter
	return d
}

// ListDevices reads the agents of the property and how many enrolment codes are open (pos.manage, or pos.cashier to see whether printing works).
func (s *Service) ListDevices(ctx context.Context, propertyID int64) (DeviceList, error) {
	p, err := s.canRead(ctx, propertyID, auth.PermPOSManage, auth.PermPOSCashier)
	if err != nil {
		return DeviceList{}, err
	}
	q := s.q(ctx)
	now := s.clock.Now()
	rows, err := q.ListDevices(ctx, posdb.ListDevicesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return DeviceList{}, err
	}
	out := DeviceList{Data: make([]Device, len(rows))}
	for i, r := range rows {
		out.Data[i] = s.toDevice(r, now)
	}
	pend, err := q.PendingEnrollments(ctx, posdb.PendingEnrollmentsParams{TenantID: p.TenantID, PropertyID: propertyID, Now: now})
	if err != nil {
		return DeviceList{}, err
	}
	out.PendingEnrollments = int(pend.Pending)
	if pend.Pending > 0 {
		t := pend.LastExpiry
		out.EnrollmentExpires = &t
	}
	return out, nil
}

// CreateEnrollment makes a one-time code that enrols an agent within fifteen minutes (pos.manage). The code is shown once; only its hash is kept.
func (s *Service) CreateEnrollment(ctx context.Context, propertyID int64) (Enrollment, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Enrollment{}, err
	}
	var out Enrollment
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		code, err := newEnrollmentCode()
		if err != nil {
			return err
		}
		now := s.clock.Now()
		exp := now.Add(EnrollmentTTL)
		id, err := s.q(ctx).InsertEnrollment(ctx, posdb.InsertEnrollmentParams{TenantID: p.TenantID, PropertyID: propertyID, CodeHash: hashOf(normalEnrollmentCode(code)), ExpiresAt: exp, CreatedBy: p.ActorID(), Now: now})
		if err != nil {
			return err
		}
		out = Enrollment{Code: code, ExpiresAt: exp, Seconds: int(EnrollmentTTL.Seconds())}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "pos.device_enrollment_created", EntityType: "pos_device", EntityID: id,
			New: map[string]any{"expires_at": exp}})
	})
	return out, err
}

// RevokeDevice ends the token of an agent (pos.manage): the agent is answered 401 and stops. Its printers stay assigned to it and wait for another agent.
func (s *Service) RevokeDevice(ctx context.Context, propertyID, id int64) (Device, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Device{}, err
	}
	var out Device
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		n, err := q.RevokeDevice(ctx, posdb.RevokeDeviceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err := q.DeviceState(ctx, posdb.DeviceStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
				return orNotFound(err, notFound("DEVICE_NOT_FOUND", "device"))
			}
			return apperr.Conflict("DEVICE_REVOKED", "the device is already revoked")
		}
		if out, err = s.deviceOf(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "pos.device_revoked", EntityType: "pos_device", EntityID: id, EntityLabel: out.Name})
	})
	return out, err
}

// RotateDeviceToken replaces the token of an agent (pos.manage): the old one stops at once; the new one is shown once and is typed or pasted
// into the agent's configuration.
func (s *Service) RotateDeviceToken(ctx context.Context, propertyID, id int64) (DeviceToken, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return DeviceToken{}, err
	}
	var out DeviceToken
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		token, prefix, hash, err := newDeviceToken()
		if err != nil {
			return err
		}
		q := s.q(ctx)
		n, err := q.RotateDevice(ctx, posdb.RotateDeviceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, TokenHash: hash, TokenPrefix: prefix, Now: s.clock.Now()})
		if err != nil {
			return err
		}
		if n == 0 {
			if _, err := q.DeviceState(ctx, posdb.DeviceStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
				return orNotFound(err, notFound("DEVICE_NOT_FOUND", "device"))
			}
			return apperr.Conflict("DEVICE_REVOKED", "a revoked device has no token to replace: enrol it again")
		}
		d, err := s.deviceOf(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		out = DeviceToken{Device: d, Token: token}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "pos.device_token_rotated", EntityType: "pos_device", EntityID: id, EntityLabel: d.Name})
	})
	return out, err
}

func (s *Service) deviceOf(ctx context.Context, tenantID, propertyID, id int64) (Device, error) {
	rows, err := s.q(ctx).ListDevices(ctx, posdb.ListDevicesParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return Device{}, err
	}
	for _, r := range rows {
		if r.ID == id {
			return s.toDevice(r, s.clock.Now()), nil
		}
	}
	return Device{}, notFound("DEVICE_NOT_FOUND", "device")
}

// checkDevice says whether a printer may be assigned to the device: it exists in the property and is not revoked.
func (s *Service) checkDevice(ctx context.Context, p auth.Principal, propertyID int64, id *int64) error {
	if id == nil {
		return nil
	}
	st, err := s.q(ctx).DeviceState(ctx, posdb.DeviceStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *id})
	if err != nil {
		return orNotFound(err, invalid("printer", fieldErr("device_id", "NOT_FOUND", "no such device in this property")))
	}
	if st.Status != "ACTIVE" {
		return invalid("printer", fieldErr("device_id", "INACTIVE", "the device is revoked"))
	}
	return nil
}

// ---------------------------------------------------------------- the device side

// Enroll turns a code into a device and its token (no user, no token yet). One code makes one device; a wrong, used or old code is one answer
// so a guess learns nothing.
func (s *Service) Enroll(ctx context.Context, code, name, version string) (EnrolledDevice, error) {
	name = strings.TrimSpace(name)
	if f := checkName("name", name); len(f) > 0 {
		return EnrolledDevice{}, invalid("device", f...)
	}
	version = truncate(strings.TrimSpace(version), 40)
	bad := apperr.New(apperr.KindInvalid, "ENROLLMENT_CODE_INVALID", "the enrolment code is wrong, used or expired: ask for a new one")
	var out EnrolledDevice
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		e, err := q.EnrollmentForUpdate(ctx, hashOf(normalEnrollmentCode(code)))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return bad
			}
			return err
		}
		if e.UsedAt != nil || !now.Before(e.ExpiresAt) {
			return bad
		}
		token, prefix, hash, err := newDeviceToken()
		if err != nil {
			return err
		}
		var ver *string
		if version != "" {
			ver = &version
		}
		id, err := q.InsertDevice(ctx, posdb.InsertDeviceParams{TenantID: e.TenantID, PropertyID: e.PropertyID, Name: name, TokenHash: hash, TokenPrefix: prefix, AgentVersion: ver, Now: now})
		if err != nil {
			return err
		}
		if err := q.UseEnrollment(ctx, posdb.UseEnrollmentParams{ID: e.ID, DeviceID: &id, Now: now}); err != nil {
			return err
		}
		out = EnrolledDevice{DeviceID: id, Name: name, Token: token, PollSeconds: PollSeconds, PropertyID: e.PropertyID}
		pid := e.PropertyID
		return s.audit.Write(ctx, audit.Entry{TenantID: e.TenantID, PropertyID: &pid, Action: "pos.device_enrolled", EntityType: "pos_device", EntityID: id, EntityLabel: name})
	})
	return out, err
}

// AuthenticateDevice finds the device of a token. A user's token, a wrong one and a revoked one are all 401: the agent of a revoked token stops.
func (s *Service) AuthenticateDevice(ctx context.Context, token string) (DeviceIdentity, error) {
	if !strings.HasPrefix(token, DeviceTokenPrefix) {
		return DeviceIdentity{}, apperr.Unauthorized("DEVICE_TOKEN_INVALID", "a device token is required")
	}
	d, err := s.q(ctx).DeviceByToken(ctx, hashOf(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DeviceIdentity{}, apperr.Unauthorized("DEVICE_TOKEN_INVALID", "the device token is not known: enrol the agent again")
		}
		return DeviceIdentity{}, err
	}
	if d.Status != "ACTIVE" {
		return DeviceIdentity{}, apperr.Unauthorized("DEVICE_REVOKED", "the device was revoked: enrol the agent again")
	}
	return DeviceIdentity{ID: d.ID, TenantID: d.TenantID, PropertyID: d.PropertyID, Name: d.Name}, nil
}

// DeviceConfig is the printers the agent serves and the poll it should use.
func (s *Service) DeviceConfig(ctx context.Context, dev DeviceIdentity) (DeviceConfig, error) {
	rows, err := s.q(ctx).DevicePrinters(ctx, posdb.DevicePrintersParams{PropertyID: dev.PropertyID, DeviceID: &dev.ID})
	if err != nil {
		return DeviceConfig{}, err
	}
	out := DeviceConfig{DeviceID: dev.ID, Name: dev.Name, PropertyID: dev.PropertyID, PollSeconds: PollSeconds, ServerTime: s.clock.Now(), Printers: make([]DevicePrinter, len(rows))}
	for i, r := range rows {
		out.Printers[i] = DevicePrinter{ID: r.ID, Name: r.Name, Kind: r.Kind, ConnectionInfo: r.ConnectionInfo, PaperWidthMM: int(r.PaperWidthMm), OpensDrawer: r.OpensDrawer}
	}
	return out, nil
}

// PrinterReport is the status of one printer in a heartbeat.
type PrinterReport struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
}

// Heartbeat is what an agent says every 30 seconds.
type Heartbeat struct {
	Printers     []PrinterReport `json:"printers"`
	QueueDepth   *int            `json:"queue_depth"`
	AgentVersion string          `json:"agent_version"`
}

var printerStatuses = map[string]bool{"ONLINE": true, "OFFLINE": true, "PAPER_OUT": true, "COVER_OPEN": true}

// Heartbeat records that the agent is alive and the status of its printers, and answers the configuration.
func (s *Service) Heartbeat(ctx context.Context, dev DeviceIdentity, in Heartbeat) (DeviceConfig, error) {
	if len(in.Printers) > 50 {
		return DeviceConfig{}, invalid("heartbeat", fieldErr("printers", "INVALID_VALUE", "at most 50 printers"))
	}
	for i, pr := range in.Printers {
		if !printerStatuses[pr.Status] {
			return DeviceConfig{}, invalid("heartbeat", fieldErr("printers["+strconv.Itoa(i)+"].status", "INVALID_VALUE", "ONLINE, OFFLINE, PAPER_OUT or COVER_OPEN"))
		}
	}
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		if err := s.touchDevice(ctx, dev, in.AgentVersion, in.QueueDepth); err != nil {
			return err
		}
		for _, pr := range in.Printers {
			if _, err := q.ReportPrinterStatus(ctx, posdb.ReportPrinterStatusParams{PropertyID: dev.PropertyID, ID: pr.ID, DeviceID: &dev.ID, Status: &pr.Status, Now: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return DeviceConfig{}, err
	}
	return s.DeviceConfig(ctx, dev)
}

func (s *Service) touchDevice(ctx context.Context, dev DeviceIdentity, version string, depth *int) error {
	var v *string
	if version = truncate(strings.TrimSpace(version), 40); version != "" {
		v = &version
	}
	var d pgtype.Int4
	if depth != nil && *depth >= 0 {
		d = pgtype.Int4{Int32: int32(min(*depth, 1_000_000)), Valid: true} //nolint:gosec // G115: bounded
	}
	return s.q(ctx).DeviceSeen(ctx, posdb.DeviceSeenParams{ID: dev.ID, Now: s.clock.Now(), AgentVersion: v, QueueDepth: d})
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

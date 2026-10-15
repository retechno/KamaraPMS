package pos

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/pos/posdb"
)

// The realtime of the POS (docs/architecture/34-pos.md 10.1, 19.1): the screens read a stream of events that say *what changed*, never the
// data; a screen reads the data with its own permission. The events are rows of pos_events written by triggers in the transaction that
// changed the thing, so a use case cannot forget one and an event never exists for a change that rolled back.

const (
	// EventBatch is the most events one read of the outbox returns.
	EventBatch = 200
	// EventRetention is how long the outbox keeps a row: a client that is further behind reloads (410).
	EventRetention = 48 * time.Hour
	// MaxChannels is the most channels one stream may ask for.
	MaxChannels = 8
	// MaxStreamsPerUser and MaxStreams bound the open connections (each is a goroutine and a socket, not a database connection).
	MaxStreamsPerUser = 6
	MaxStreams        = 400
	// StreamLifetime is how long one stream lives: the token that opened it is short-lived, so the client reconnects with a fresh one.
	StreamLifetime = 10 * time.Minute
)

// Event is one thing that changed, as the stream sends it.
type Event struct {
	ID       int64     `json:"id"`
	Channel  string    `json:"channel"`
	Type     string    `json:"type"`
	EntityID int64     `json:"entity_id"`
	At       time.Time `json:"at"`
}

// EventCursor is a position in the outbox: events are read in the order (TxID, ID), see migration 00082.
type EventCursor struct{ TxID, ID int64 }

var channelPattern = regexp.MustCompile(`^(outlet|station):([0-9]{1,18})$`)

// ErrEventsGone is a resume point the outbox no longer holds (older than the retention, or of another property): the client reloads.
var ErrEventsGone = apperr.Gone("EVENTS_GONE", "the stream cannot resume from that event; reload the screen")

// ErrTooManyStreams is the limit of open streams.
var ErrTooManyStreams = apperr.New(apperr.KindRateLimited, "TOO_MANY_STREAMS", "too many open streams for this user")

// ---------------------------------------------------------------- the hub: one wake-up for all the streams of the process

// Hub wakes the streams when a transaction that wrote an event commits. It holds one connection that LISTENs while any stream is open,
// and nothing else: each stream reads its own events with the cursor it keeps, so a missed wake-up delays and never loses (a stream also
// reads on a short timer).
type Hub struct {
	mu      sync.Mutex
	wake    chan struct{}
	streams int
	perUser map[int64]int
	cancel  context.CancelFunc
	pool    *pgxpool.Pool
}

func newHub() *Hub { return &Hub{wake: make(chan struct{}), perUser: map[int64]int{}} }

// SetPool gives the hub the pool whose connection it listens on (without it, the streams read on their timer only).
func (h *Hub) SetPool(p *pgxpool.Pool) { h.mu.Lock(); h.pool = p; h.mu.Unlock() }

// Wait returns a channel that is closed at the next wake-up.
func (h *Hub) Wait() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.wake
}

func (h *Hub) notify() {
	h.mu.Lock()
	close(h.wake)
	h.wake = make(chan struct{})
	h.mu.Unlock()
}

// Open registers a stream of a user; the returned function ends it. It answers ErrTooManyStreams at the limits.
func (h *Hub) Open(userID int64) (func(), error) {
	h.mu.Lock()
	if h.streams >= MaxStreams || h.perUser[userID] >= MaxStreamsPerUser {
		h.mu.Unlock()
		return nil, ErrTooManyStreams
	}
	h.streams++
	h.perUser[userID]++
	if h.streams == 1 && h.pool != nil {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go h.listen(ctx, h.pool)
	}
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			h.streams--
			if h.perUser[userID]--; h.perUser[userID] <= 0 {
				delete(h.perUser, userID)
			}
			if h.streams == 0 && h.cancel != nil {
				h.cancel()
				h.cancel = nil
			}
			h.mu.Unlock()
		})
	}, nil
}

// listen holds the LISTEN connection until ctx ends, and reconnects with a backoff when it breaks.
func (h *Hub) listen(ctx context.Context, pool *pgxpool.Pool) {
	backoff := time.Second
	for ctx.Err() == nil {
		err := h.listenOnce(ctx, pool)
		if ctx.Err() != nil {
			return
		}
		_ = err
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
		h.notify() // the streams read once more: what was missed while the connection was down
	}
}

func (h *Hub) listenOnce(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN pos_events"); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN *") }()
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		h.notify()
	}
}

// Hub returns the hub of the service.
func (s *Service) Hub() *Hub { return s.hub }

// SetEventPool lets the hub LISTEN (wired in internal/app); a service without it works on the timers of the streams.
func (s *Service) SetEventPool(p *pgxpool.Pool) { s.hub.SetPool(p) }

// ---------------------------------------------------------------- authorising and reading

// RequireStreamReader asks for any permission that has a channel; the channels are then checked one by one. It is asked first, so that a caller who may listen to
// nothing is told so whatever channels they named.
func (s *Service) RequireStreamReader(ctx context.Context, propertyID int64) error {
	if _, err := auth.Require(ctx); err != nil {
		return err
	}
	return auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSManage, auth.PermPOSOrder, auth.PermPOSCashier, auth.PermPOSKDS, auth.PermFolioOutletChargeOpen, auth.PermFolioRead)
}

// ParseChannels checks the names of the channels asked for: outlet:{id}, station:{id}, frontdesk, printers.
func ParseChannels(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, c := range strings.Split(raw, ",") {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			continue
		}
		if c != "frontdesk" && c != "printers" && !channelPattern.MatchString(c) {
			return nil, apperr.Invalid("the channels are invalid", fieldErr("channels", "INVALID_VALUE", "outlet:{id}, station:{id}, frontdesk or printers"))
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) == 0 || len(out) > MaxChannels {
		return nil, apperr.Invalid("the channels are invalid", fieldErr("channels", "REQUIRED", "1 to 8 channels"))
	}
	return out, nil
}

// AuthoriseChannels checks that the caller may listen to each channel and that the outlet or station is of the property (404 otherwise: the
// same answer for another tenant's id and for one that does not exist).
func (s *Service) AuthoriseChannels(ctx context.Context, propertyID int64, channels []string) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	q := s.q(ctx)
	for _, c := range channels {
		switch {
		case c == "frontdesk":
			err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermFolioOutletChargeOpen, auth.PermFolioRead)
		case c == "printers":
			err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSManage, auth.PermPOSCashier)
		case strings.HasPrefix(c, "outlet:"):
			if err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSManage, auth.PermPOSOrder, auth.PermPOSCashier); err != nil {
				break
			}
			id, _ := strconv.ParseInt(strings.TrimPrefix(c, "outlet:"), 10, 64)
			var ok bool
			if ok, err = q.OutletExists(ctx, posdb.OutletExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err == nil && !ok {
				err = notFound("OUTLET_NOT_FOUND", "outlet")
			}
		case strings.HasPrefix(c, "station:"):
			if err = auth.RequireAny(ctx, s.authz, propertyID, auth.PermPOSManage, auth.PermPOSKDS); err != nil {
				break
			}
			id, _ := strconv.ParseInt(strings.TrimPrefix(c, "station:"), 10, 64)
			var ok bool
			if ok, err = q.StationExists(ctx, posdb.StationExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err == nil && !ok {
				err = notFound("STATION_NOT_FOUND", "station")
			}
		}
		if err != nil {
			return p, err
		}
	}
	return p, nil
}

// EventsStart is the cursor a stream begins at: the one of Last-Event-ID, or the head of the outbox. ErrEventsGone when the id is not held.
func (s *Service) EventsStart(ctx context.Context, propertyID int64, lastID int64) (EventCursor, error) {
	q := s.q(ctx)
	if lastID > 0 {
		pos, err := q.EventPosition(ctx, posdb.EventPositionParams{PropertyID: propertyID, ID: lastID})
		if errors.Is(err, pgx.ErrNoRows) {
			return EventCursor{}, ErrEventsGone
		}
		if err != nil {
			return EventCursor{}, err
		}
		return EventCursor{TxID: pos.Txid, ID: pos.ID}, nil
	}
	h, err := q.EventHead(ctx, propertyID)
	if err != nil {
		return EventCursor{}, err
	}
	return EventCursor{TxID: h.Txid, ID: h.ID}, nil
}

// EventsSince reads the final events after the cursor, in order, and the cursor after them.
func (s *Service) EventsSince(ctx context.Context, tenantID, propertyID int64, channels []string, cur EventCursor) ([]Event, EventCursor, error) {
	rows, err := s.q(ctx).EventsAfter(ctx, posdb.EventsAfterParams{TenantID: tenantID, PropertyID: propertyID, Channels: channels, AfterTxid: cur.TxID, AfterID: cur.ID, MaxRows: EventBatch})
	if err != nil {
		return nil, cur, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, Event{ID: r.ID, Channel: r.Channel, Type: r.Type, EntityID: r.EntityID, At: r.At})
		cur = EventCursor{TxID: r.Txid, ID: r.ID}
	}
	return out, cur, nil
}

// maybePurge deletes the old rows of the outbox at most once in ten minutes by process, from the first stream that opens after.
func (s *Service) maybePurge(ctx context.Context) {
	now := s.clock.Now()
	s.purgeMu.Lock()
	due := now.Sub(s.purgedAt) > 10*time.Minute
	if due {
		s.purgedAt = now
	}
	s.purgeMu.Unlock()
	if due {
		_, _ = s.PurgeEvents(ctx)
	}
}

// PurgeEvents deletes the rows older than the retention; the streams call it now and then (it is cheap: an index on the time).
func (s *Service) PurgeEvents(ctx context.Context) (int64, error) {
	return s.q(ctx).PurgeEvents(ctx, s.clock.Now().Add(-EventRetention))
}

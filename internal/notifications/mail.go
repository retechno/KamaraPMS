// Package notifications sends e-mail. A message is first queued in the database (the outbox), in the transaction
// that causes it, and sent by a background worker: a mail server that is slow, down or refusing never blocks or
// fails the booking, and a queued message survives a restart.
package notifications

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strings"
	"time"
)

// Attachment is a file sent with a message.
type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// Message is an e-mail to one recipient: a text body, an HTML alternative and attachments.
type Message struct {
	To          string
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// SMTPConfig is the mail server. Without a Host, e-mail is off.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // the sender address
	FromName string
	TLS      string // "starttls" (default), "tls" (implicit TLS, usually port 465) or "none"
}

// Enabled reports whether a mail server is configured.
func (c SMTPConfig) Enabled() bool { return c.Host != "" }

// Validate checks the settings that are given.
func (c SMTPConfig) Validate() error {
	if !c.Enabled() {
		return nil
	}
	if c.Port < 1 || c.Port > 65535 {
		return errors.New("PMS_SMTP_PORT: must be between 1 and 65535")
	}
	if _, err := mail.ParseAddress(c.From); err != nil {
		return errors.New("PMS_SMTP_FROM: required with PMS_SMTP_HOST, an e-mail address")
	}
	switch c.TLS {
	case "starttls", "tls", "none":
	default:
		return fmt.Errorf("PMS_SMTP_TLS: must be starttls, tls or none, got %q", c.TLS)
	}
	return nil
}

// SMTPSender delivers through an SMTP server.
type SMTPSender struct{ cfg SMTPConfig }

// NewSMTPSender returns a sender for a validated configuration.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender { return &SMTPSender{cfg: cfg} }

// ErrPermanent marks a refusal that retrying cannot fix (a bad recipient, for instance).
var ErrPermanent = errors.New("permanent failure")

// Send connects, authenticates when a user name is set, and sends one message. The connection is bounded in
// time by the context and a fixed ceiling.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	raw, err := Build(m, s.cfg.From, s.cfg.FromName, time.Now()) //nolint:forbidigo // the Date header of a message is the instant it is built
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprint(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if s.cfg.TLS == "tls" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}}).DialContext(ctx, "tcp", addr)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(60 * time.Second) //nolint:forbidigo // a network timeout, not a hotel date
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer c.Close() //nolint:errcheck // the message was already accepted or the error returned
	if s.cfg.TLS == "starttls" {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return err
		}
	}
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	if err := c.Mail(from.Address); err != nil {
		return err
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	if err := c.Rcpt(to.Address); err != nil {
		return classify(err)
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(raw); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return classify(err)
	}
	return c.Quit()
}

// classify turns a 5xx reply into a permanent failure: the server refused the message, retrying will not help.
func classify(err error) error {
	var te *textproto.Error
	if errors.As(err, &te) && te.Code >= 500 && te.Code < 600 {
		return fmt.Errorf("%w: %w", ErrPermanent, err)
	}
	return err
}

// Build renders a message as RFC 5322 text: multipart/mixed (body and attachments) around multipart/alternative
// (text and HTML). Header values cannot carry a line break, so a hostile subject or name cannot add headers.
func Build(m Message, from, fromName string, now time.Time) ([]byte, error) {
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("recipient: %w", err)
	}
	sender, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("sender: %w", err)
	}
	if fromName != "" {
		sender.Name = fromName
	}
	var b bytes.Buffer
	hdr := func(k, v string) { b.WriteString(k + ": " + oneLine(v) + "\r\n") }
	hdr("From", sender.String())
	hdr("To", to.String())
	hdr("Subject", mime.QEncoding.Encode("utf-8", oneLine(m.Subject)))
	hdr("Date", now.UTC().Format(time.RFC1123Z))
	hdr("Message-ID", "<"+token(12)+"@"+domainOf(sender.Address)+">")
	hdr("MIME-Version", "1.0")
	hdr("Auto-Submitted", "auto-generated")
	outer, inner := token(12), token(12)
	if len(m.Attachments) > 0 {
		hdr("Content-Type", `multipart/mixed; boundary="`+outer+`"`)
		b.WriteString("\r\n--" + outer + "\r\n")
	}
	if m.HTML != "" {
		b.WriteString(`Content-Type: multipart/alternative; boundary="` + inner + "\"\r\n\r\n")
		part(&b, inner, "text/plain; charset=utf-8", m.Text)
		part(&b, inner, "text/html; charset=utf-8", m.HTML)
		b.WriteString("--" + inner + "--\r\n")
	} else {
		if len(m.Attachments) == 0 {
			hdr("Content-Type", "text/plain; charset=utf-8")
			hdr("Content-Transfer-Encoding", "base64")
			b.WriteString("\r\n" + wrap76(base64.StdEncoding.EncodeToString([]byte(m.Text))))
			return b.Bytes(), nil
		}
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n" + wrap76(base64.StdEncoding.EncodeToString([]byte(m.Text))))
	}
	for _, a := range m.Attachments {
		name := mime.QEncoding.Encode("utf-8", oneLine(a.Filename))
		b.WriteString("\r\n--" + outer + "\r\n")
		b.WriteString("Content-Type: " + oneLine(a.ContentType) + `; name="` + name + "\"\r\n")
		b.WriteString("Content-Transfer-Encoding: base64\r\n")
		b.WriteString(`Content-Disposition: attachment; filename="` + name + "\"\r\n\r\n")
		b.WriteString(wrap76(base64.StdEncoding.EncodeToString(a.Data)))
	}
	if len(m.Attachments) > 0 {
		b.WriteString("\r\n--" + outer + "--\r\n")
	}
	return b.Bytes(), nil
}

func part(b *bytes.Buffer, boundary, contentType, body string) {
	b.WriteString("--" + boundary + "\r\nContent-Type: " + contentType + "\r\nContent-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrap76(base64.StdEncoding.EncodeToString([]byte(body))))
}

func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76] + "\r\n")
		s = s[76:]
	}
	b.WriteString(s + "\r\n")
	return b.String()
}

func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func token(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return "localhost"
}

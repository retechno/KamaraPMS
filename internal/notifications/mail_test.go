package notifications

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"strings"
	"sync"
	"testing"
	"time"
)

var epoch = time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)

func TestBuildIsAWellFormedMultipartMessage(t *testing.T) {
	raw, err := Build(Message{
		To: "siti@example.test", Subject: "Reservation confirmation RES000009 - Hôtel Bali", Text: "Dear Siti,\nthanks", HTML: "<p>Dear Siti</p>",
		Attachments: []Attachment{{Filename: "confirmation-RES000009.pdf", ContentType: "application/pdf", Data: []byte("%PDF-1.3 fake")}},
	}, "frontdesk@bali.test", "Hotel Bali", epoch)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	dec := new(mime.WordDecoder)
	if subj, _ := dec.DecodeHeader(msg.Header.Get("Subject")); subj != "Reservation confirmation RES000009 - Hôtel Bali" {
		t.Fatalf("subject: %q", subj)
	}
	if from := msg.Header.Get("From"); !strings.Contains(from, "Hotel Bali") || !strings.Contains(from, "<frontdesk@bali.test>") {
		t.Fatalf("from: %q", from)
	}
	if msg.Header.Get("To") != "<siti@example.test>" || msg.Header.Get("Date") == "" || msg.Header.Get("Message-ID") == "" || msg.Header.Get("Auto-Submitted") != "auto-generated" {
		t.Fatalf("headers: %v", msg.Header)
	}
	mt, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil || mt != "multipart/mixed" {
		t.Fatalf("content type: %q %v", mt, err)
	}
	outer := multipart.NewReader(msg.Body, params["boundary"])
	body, err := outer.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	amt, aparams, _ := mime.ParseMediaType(body.Header.Get("Content-Type"))
	if amt != "multipart/alternative" {
		t.Fatalf("first part: %s", amt)
	}
	alt := multipart.NewReader(body, aparams["boundary"])
	for _, want := range []string{"Dear Siti,\nthanks", "<p>Dear Siti</p>"} {
		p, err := alt.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		if string(b) != want {
			t.Fatalf("alternative: %q", b)
		}
	}
	att, err := outer.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, att))
	if att.FileName() != "confirmation-RES000009.pdf" || string(b) != "%PDF-1.3 fake" {
		t.Fatalf("attachment: %q %q", att.FileName(), b)
	}
}

func TestBuildCannotBeUsedToInjectHeaders(t *testing.T) {
	raw, err := Build(Message{To: "siti@example.test", Subject: "hi\r\nBcc: victim@example.test", Text: "x"}, "a@b.test", "Name\r\nX-Evil: 1", epoch)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if msg.Header.Get("Bcc") != "" || msg.Header.Get("X-Evil") != "" {
		t.Fatalf("injected headers: %v", msg.Header)
	}
	for _, bad := range []string{"not an address", "a@b.test\r\nBcc: x@y.test", ""} {
		if _, err := Build(Message{To: bad, Subject: "s", Text: "t"}, "a@b.test", "", epoch); err == nil {
			t.Errorf("recipient %q must be refused", bad)
		}
	}
}

func TestPlainTextMessageHasNoMultipart(t *testing.T) {
	raw, err := Build(Message{To: "siti@example.test", Subject: "s", Text: "just text"}, "a@b.test", "", epoch)
	if err != nil {
		t.Fatal(err)
	}
	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	b, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, msg.Body))
	if !strings.HasPrefix(msg.Header.Get("Content-Type"), "text/plain") || string(b) != "just text" {
		t.Fatalf("%v %q", msg.Header, b)
	}
}

// smtpServer is a minimal SMTP server for the tests: it records one message and can refuse the recipient.
type smtpServer struct {
	ln       net.Listener
	mu       sync.Mutex
	auth     string
	from     string
	to       []string
	data     string
	refuseTo bool
}

func newSMTPServer(t *testing.T) *smtpServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &smtpServer{ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	return s
}

func (s *smtpServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *smtpServer) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	say := func(l string) { _, _ = c.Write([]byte(l + "\r\n")) }
	say("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)
		s.mu.Lock()
		switch {
		case strings.HasPrefix(up, "EHLO"):
			say("250-test")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(up, "AUTH PLAIN"):
			s.auth = strings.TrimSpace(line[len("AUTH PLAIN"):])
			say("235 ok")
		case strings.HasPrefix(up, "MAIL FROM:"):
			s.from = line
			say("250 ok")
		case strings.HasPrefix(up, "RCPT TO:"):
			if s.refuseTo {
				say("550 no such user")
			} else {
				s.to = append(s.to, line)
				say("250 ok")
			}
		case up == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			s.data = b.String()
			say("250 queued")
		case up == "QUIT":
			say("221 bye")
			s.mu.Unlock()
			return
		default:
			say("250 ok")
		}
		s.mu.Unlock()
	}
}

func TestSMTPSenderDeliversWithAuthentication(t *testing.T) {
	srv := newSMTPServer(t)
	snd := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), Username: "user", Password: "secret", From: "frontdesk@bali.test", FromName: "Hotel Bali", TLS: "none"})
	err := snd.Send(context.Background(), Message{To: "siti@example.test", Subject: "Hello", Text: "body", Attachments: []Attachment{{Filename: "a.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}}})
	if err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	cred, _ := base64.StdEncoding.DecodeString(srv.auth)
	if string(cred) != "\x00user\x00secret" {
		t.Fatalf("auth: %q", cred)
	}
	if !strings.Contains(srv.from, "frontdesk@bali.test") || len(srv.to) != 1 || !strings.Contains(srv.to[0], "siti@example.test") {
		t.Fatalf("envelope: %q %q", srv.from, srv.to)
	}
	if !strings.Contains(srv.data, "Subject: Hello") || !strings.Contains(srv.data, "filename=\"a.pdf\"") {
		t.Fatalf("data: %s", srv.data)
	}
}

func TestSMTPRefusalsArePermanentAndOutagesAreNot(t *testing.T) {
	srv := newSMTPServer(t)
	srv.refuseTo = true
	snd := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), From: "frontdesk@bali.test", TLS: "none"})
	err := snd.Send(context.Background(), Message{To: "nobody@example.test", Subject: "s", Text: "t"})
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("a 550 for the recipient is permanent: %v", err)
	}
	if err := snd.Send(context.Background(), Message{To: "not an address", Subject: "s", Text: "t"}); !errors.Is(err, ErrPermanent) {
		t.Fatalf("a bad address is permanent: %v", err)
	}
	_ = srv.ln.Close()
	down := NewSMTPSender(SMTPConfig{Host: "127.0.0.1", Port: srv.port(), From: "frontdesk@bali.test", TLS: "none"})
	if err := down.Send(context.Background(), Message{To: "siti@example.test", Subject: "s", Text: "t"}); err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("a server that is down is worth retrying: %v", err)
	}
}

func TestSMTPConfigValidation(t *testing.T) {
	if err := (SMTPConfig{}).Validate(); err != nil {
		t.Fatalf("off is valid: %v", err)
	}
	ok := SMTPConfig{Host: "smtp.test", Port: 587, From: "a@b.test", TLS: "starttls"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*SMTPConfig){"port": func(c *SMTPConfig) { c.Port = 0 }, "from": func(c *SMTPConfig) { c.From = "" }, "tls": func(c *SMTPConfig) { c.TLS = "ssl" }} {
		c := ok
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}

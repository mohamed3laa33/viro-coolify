// Package notify provides a small, transport-agnostic email notification layer
// for Vortex. It exposes a Mailer interface with several implementations:
//
//   - SMTPMailer sends mail over SMTP via the standard library (net/smtp).
//   - NoopMailer records messages and returns nil; used when SMTP is not
//     configured so the rest of the system can run without a mail server.
//   - RecordingMailer captures messages for assertions in tests.
//
// The package depends only on the Go standard library.
package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"
)

// Message is a single email to be delivered. HTMLBody and TextBody are
// alternative representations of the same content; when both are present the
// composed message is a MIME multipart/alternative so clients can pick one.
type Message struct {
	To       string
	Subject  string
	HTMLBody string
	TextBody string
}

// Mailer delivers a Message. Implementations must be safe for concurrent use.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}

// Config configures NewMailer. When Host is empty NewMailer returns a
// NoopMailer; otherwise it returns an SMTPMailer.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	// StartTLS upgrades the plaintext connection with STARTTLS before auth.
	StartTLS bool
}

// Addr returns the host:port dial address for the configured server.
func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, fmt.Sprintf("%d", c.Port))
}

// NewMailer returns an SMTPMailer when cfg.Host is set, otherwise a NoopMailer.
func NewMailer(cfg Config) Mailer {
	if strings.TrimSpace(cfg.Host) == "" {
		return NewNoopMailer()
	}
	return NewSMTPMailer(cfg)
}

// NoopMailer records every message it is asked to send and always succeeds.
// It is used when SMTP is unconfigured so callers can run end to end without a
// real mail server. The recorded messages are exported for assertions.
type NoopMailer struct {
	mu sync.Mutex
	// Sent holds every message passed to Send, in order. Read it under no lock
	// only after sending has quiesced; use Messages for a safe copy.
	Sent []Message
}

// NewNoopMailer returns a ready-to-use NoopMailer.
func NewNoopMailer() *NoopMailer { return &NoopMailer{} }

// Send records m and returns nil.
func (n *NoopMailer) Send(_ context.Context, m Message) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Sent = append(n.Sent, m)
	return nil
}

// Messages returns a copy of the recorded messages.
func (n *NoopMailer) Messages() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Message, len(n.Sent))
	copy(out, n.Sent)
	return out
}

// Reset clears recorded messages.
func (n *NoopMailer) Reset() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Sent = nil
}

// RecordingMailer is a test double that records messages and can be configured
// to return a fixed error from Send.
type RecordingMailer struct {
	mu sync.Mutex
	// Err, when non-nil, is returned by Send (after recording the message).
	Err  error
	sent []Message
}

// NewRecordingMailer returns a ready-to-use RecordingMailer.
func NewRecordingMailer() *RecordingMailer { return &RecordingMailer{} }

// Send records m and returns the configured Err (nil by default).
func (r *RecordingMailer) Send(_ context.Context, m Message) error {
	r.mu.Lock()
	r.sent = append(r.sent, m)
	err := r.Err
	r.mu.Unlock()
	return err
}

// Messages returns a copy of the recorded messages.
func (r *RecordingMailer) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Message, len(r.sent))
	copy(out, r.sent)
	return out
}

// Count returns the number of recorded messages.
func (r *RecordingMailer) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sent)
}

// Last returns the most recently recorded message and whether one exists.
func (r *RecordingMailer) Last() (Message, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sent) == 0 {
		return Message{}, false
	}
	return r.sent[len(r.sent)-1], true
}

// Reset clears recorded messages and the configured error.
func (r *RecordingMailer) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = nil
	r.Err = nil
}

// sendFunc matches the signature of smtp.SendMail. Injecting it lets tests
// capture the composed RFC 5322 message without a live SMTP server.
type sendFunc func(addr string, a smtp.Auth, from string, to []string, msg []byte) error

// SMTPMailer delivers mail over SMTP using the standard library.
type SMTPMailer struct {
	cfg  Config
	send sendFunc
	// now is injectable so tests can assert a deterministic Date header.
	now func() time.Time
}

// NewSMTPMailer builds an SMTPMailer from cfg using smtp.SendMail as the
// transport.
func NewSMTPMailer(cfg Config) *SMTPMailer {
	return &SMTPMailer{cfg: cfg, send: smtp.SendMail, now: time.Now}
}

// auth returns the SMTP auth to use, or nil when no username is configured.
func (s *SMTPMailer) auth() smtp.Auth {
	if s.cfg.Username == "" {
		return nil
	}
	return smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
}

// Send composes m into an RFC 5322 message and hands it to the transport.
func (s *SMTPMailer) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(m.To) == "" {
		return fmt.Errorf("notify: message has no recipient")
	}

	from := s.cfg.From
	if from == "" {
		from = s.cfg.Username
	}

	msg := composeMessage(from, m, s.clock())

	send := s.send
	if send == nil {
		send = smtp.SendMail
	}
	return send(s.cfg.Addr(), s.auth(), from, []string{m.To}, msg)
}

func (s *SMTPMailer) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// boundary is a fixed MIME boundary. It only needs to be a token unlikely to
// appear in the body; a constant keeps composed output deterministic for tests.
const boundary = "vortex-notify-boundary-7f3a1c"

// composeMessage renders m as a complete RFC 5322 message. When both HTML and
// text bodies are present it emits a multipart/alternative body; when only one
// is present it emits a single-part message with the appropriate content type.
func composeMessage(from string, m Message, now time.Time) []byte {
	var buf bytes.Buffer

	writeHeader(&buf, "From", from)
	writeHeader(&buf, "To", m.To)
	writeHeader(&buf, "Subject", encodeSubject(m.Subject))
	writeHeader(&buf, "Date", now.Format(time.RFC1123Z))
	writeHeader(&buf, "MIME-Version", "1.0")

	hasHTML := m.HTMLBody != ""
	hasText := m.TextBody != ""

	switch {
	case hasHTML && hasText:
		writeHeader(&buf, "Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
		buf.WriteString("\r\n")
		writePart(&buf, "text/plain; charset=UTF-8", m.TextBody)
		writePart(&buf, "text/html; charset=UTF-8", m.HTMLBody)
		buf.WriteString("--" + boundary + "--\r\n")
	case hasHTML:
		writeHeader(&buf, "Content-Type", "text/html; charset=UTF-8")
		buf.WriteString("\r\n")
		buf.WriteString(normalizeBody(m.HTMLBody))
	default:
		// Plain text only (also the case when the message is entirely empty).
		writeHeader(&buf, "Content-Type", "text/plain; charset=UTF-8")
		buf.WriteString("\r\n")
		buf.WriteString(normalizeBody(m.TextBody))
	}

	return buf.Bytes()
}

// writeHeader writes a single "Key: value" header line with CRLF.
func writeHeader(buf *bytes.Buffer, key, value string) {
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

// writePart writes one MIME part of a multipart/alternative body.
func writePart(buf *bytes.Buffer, contentType, body string) {
	buf.WriteString("--" + boundary + "\r\n")
	writeHeader(buf, "Content-Type", contentType)
	buf.WriteString("\r\n")
	buf.WriteString(normalizeBody(body))
	buf.WriteString("\r\n")
}

// normalizeBody converts bare LF line endings to CRLF as required by RFC 5322.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}

// encodeSubject RFC 2047-encodes a subject when it contains non-ASCII bytes,
// otherwise returns it unchanged. Headers are also defended against injection
// by stripping CR/LF.
func encodeSubject(s string) string {
	s = strings.NewReplacer("\r", "", "\n", "").Replace(s)
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return mimeEncodeWord(s)
		}
	}
	return s
}

// mimeEncodeWord produces an RFC 2047 base64 encoded-word for UTF-8 text.
func mimeEncodeWord(s string) string {
	return "=?UTF-8?B?" + base64Std(s) + "?="
}

// base64Std is a tiny standard-base64 encoder kept local to avoid importing
// encoding/base64 only for header encoding; it mirrors RawStdEncoding+padding.
func base64Std(s string) string {
	const tbl = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	src := []byte(s)
	var out strings.Builder
	for i := 0; i < len(src); i += 3 {
		var b [3]byte
		n := copy(b[:], src[i:])
		out.WriteByte(tbl[b[0]>>2])
		out.WriteByte(tbl[(b[0]&0x03)<<4|b[1]>>4])
		if n > 1 {
			out.WriteByte(tbl[(b[1]&0x0f)<<2|b[2]>>6])
		} else {
			out.WriteByte('=')
		}
		if n > 2 {
			out.WriteByte(tbl[b[2]&0x3f])
		} else {
			out.WriteByte('=')
		}
	}
	return out.String()
}

// dialTLSConfig builds a tls.Config for STARTTLS connections. It is retained so
// a future explicit-connection Send path can share verification settings.
func (s *SMTPMailer) dialTLSConfig() *tls.Config {
	return &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}
}

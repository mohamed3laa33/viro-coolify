package notify

import (
	"context"
	"errors"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestNewMailerSelectsImplementation(t *testing.T) {
	tests := []struct {
		name   string
		cfg    Config
		isSMTP bool
	}{
		{name: "host set -> SMTP", cfg: Config{Host: "smtp.example.com", Port: 587}, isSMTP: true},
		{name: "empty host -> Noop", cfg: Config{Host: ""}, isSMTP: false},
		{name: "whitespace host -> Noop", cfg: Config{Host: "   "}, isSMTP: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewMailer(tt.cfg)
			switch m.(type) {
			case *SMTPMailer:
				if !tt.isSMTP {
					t.Fatalf("expected NoopMailer, got *SMTPMailer")
				}
			case *NoopMailer:
				if tt.isSMTP {
					t.Fatalf("expected *SMTPMailer, got *NoopMailer")
				}
			default:
				t.Fatalf("unexpected mailer type %T", m)
			}
		})
	}
}

func TestConfigAddr(t *testing.T) {
	got := Config{Host: "smtp.example.com", Port: 587}.Addr()
	if got != "smtp.example.com:587" {
		t.Fatalf("Addr() = %q", got)
	}
}

func TestNoopMailerRecords(t *testing.T) {
	n := NewNoopMailer()
	if err := n.Send(context.Background(), Message{To: "a@x.com", Subject: "Hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := n.Send(context.Background(), Message{To: "b@x.com"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(n.Sent) != 2 {
		t.Fatalf("Sent len = %d, want 2", len(n.Sent))
	}
	msgs := n.Messages()
	if len(msgs) != 2 || msgs[0].To != "a@x.com" || msgs[1].To != "b@x.com" {
		t.Fatalf("Messages mismatch: %+v", msgs)
	}
	// Mutating the returned copy must not affect internal state.
	msgs[0].To = "mutated"
	if n.Sent[0].To != "a@x.com" {
		t.Fatalf("Messages returned a non-copy")
	}
	n.Reset()
	if len(n.Messages()) != 0 {
		t.Fatalf("Reset did not clear messages")
	}
}

func TestRecordingMailer(t *testing.T) {
	r := NewRecordingMailer()
	if _, ok := r.Last(); ok {
		t.Fatalf("Last on empty mailer returned ok=true")
	}
	if r.Count() != 0 {
		t.Fatalf("Count = %d, want 0", r.Count())
	}

	if err := r.Send(context.Background(), Message{To: "x@y.com", Subject: "S"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if r.Count() != 1 {
		t.Fatalf("Count = %d, want 1", r.Count())
	}
	last, ok := r.Last()
	if !ok || last.Subject != "S" {
		t.Fatalf("Last = %+v ok=%v", last, ok)
	}

	// Configured error is returned but message is still recorded.
	wantErr := errors.New("boom")
	r.Err = wantErr
	if err := r.Send(context.Background(), Message{To: "z@y.com"}); !errors.Is(err, wantErr) {
		t.Fatalf("Send err = %v, want %v", err, wantErr)
	}
	if r.Count() != 2 {
		t.Fatalf("Count = %d, want 2", r.Count())
	}

	msgs := r.Messages()
	msgs[0].To = "mutated"
	again := r.Messages()
	if again[0].To != "x@y.com" {
		t.Fatalf("Messages returned a non-copy")
	}

	r.Reset()
	if r.Count() != 0 || r.Err != nil {
		t.Fatalf("Reset did not clear state")
	}
}

// capturedSend records the arguments handed to the transport.
type capturedSend struct {
	addr string
	auth smtp.Auth
	from string
	to   []string
	msg  []byte
	err  error
}

func newSMTPMailerWithCapture(cfg Config, cap *capturedSend) *SMTPMailer {
	m := NewSMTPMailer(cfg)
	m.now = func() time.Time { return time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC) }
	m.send = func(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
		cap.addr = addr
		cap.auth = a
		cap.from = from
		cap.to = to
		cap.msg = msg
		return cap.err
	}
	return m
}

func TestSMTPMailerComposesMultipart(t *testing.T) {
	var cap capturedSend
	cfg := Config{Host: "smtp.example.com", Port: 587, Username: "user@example.com", Password: "pw", From: "noreply@vortex.dev"}
	m := newSMTPMailerWithCapture(cfg, &cap)

	msg := Message{
		To:       "dest@example.com",
		Subject:  "Hello",
		HTMLBody: "<p>Hello <b>world</b></p>",
		TextBody: "Hello world",
	}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if cap.addr != "smtp.example.com:587" {
		t.Fatalf("addr = %q", cap.addr)
	}
	if cap.from != "noreply@vortex.dev" {
		t.Fatalf("from = %q", cap.from)
	}
	if len(cap.to) != 1 || cap.to[0] != "dest@example.com" {
		t.Fatalf("to = %v", cap.to)
	}
	if cap.auth == nil {
		t.Fatalf("expected non-nil auth when username configured")
	}

	out := string(cap.msg)
	mustContain(t, out, "From: noreply@vortex.dev\r\n")
	mustContain(t, out, "To: dest@example.com\r\n")
	mustContain(t, out, "Subject: Hello\r\n")
	mustContain(t, out, "Date: ")
	mustContain(t, out, "MIME-Version: 1.0\r\n")
	mustContain(t, out, `Content-Type: multipart/alternative; boundary="`+boundary+`"`)
	mustContain(t, out, "Content-Type: text/plain; charset=UTF-8")
	mustContain(t, out, "Content-Type: text/html; charset=UTF-8")
	mustContain(t, out, "Hello world")
	mustContain(t, out, "<p>Hello <b>world</b></p>")
	mustContain(t, out, "--"+boundary+"--\r\n")

	// Text part should come before HTML part (RFC: least to most rich).
	textIdx := strings.Index(out, "text/plain")
	htmlIdx := strings.Index(out, "text/html")
	if textIdx < 0 || htmlIdx < 0 || textIdx > htmlIdx {
		t.Fatalf("text part should precede html part (text=%d html=%d)", textIdx, htmlIdx)
	}
}

func TestSMTPMailerNoAuthWhenNoUsername(t *testing.T) {
	var cap capturedSend
	cfg := Config{Host: "smtp.example.com", Port: 25, From: "noreply@vortex.dev"}
	m := newSMTPMailerWithCapture(cfg, &cap)
	if err := m.Send(context.Background(), Message{To: "d@e.com", TextBody: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if cap.auth != nil {
		t.Fatalf("expected nil auth when username empty")
	}
}

func TestSMTPMailerFromFallsBackToUsername(t *testing.T) {
	var cap capturedSend
	cfg := Config{Host: "h", Port: 1, Username: "user@example.com", Password: "p"}
	m := newSMTPMailerWithCapture(cfg, &cap)
	if err := m.Send(context.Background(), Message{To: "d@e.com", TextBody: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if cap.from != "user@example.com" {
		t.Fatalf("from = %q, want username fallback", cap.from)
	}
	mustContain(t, string(cap.msg), "From: user@example.com\r\n")
}

func TestSMTPMailerSingleParts(t *testing.T) {
	tests := []struct {
		name      string
		msg       Message
		wantCT    string
		wantBody  string
		notWantCT string
	}{
		{
			name:      "html only",
			msg:       Message{To: "d@e.com", Subject: "s", HTMLBody: "<p>hi</p>"},
			wantCT:    "Content-Type: text/html; charset=UTF-8",
			wantBody:  "<p>hi</p>",
			notWantCT: "multipart/alternative",
		},
		{
			name:      "text only",
			msg:       Message{To: "d@e.com", Subject: "s", TextBody: "plain"},
			wantCT:    "Content-Type: text/plain; charset=UTF-8",
			wantBody:  "plain",
			notWantCT: "multipart/alternative",
		},
		{
			name:      "empty bodies still valid text message",
			msg:       Message{To: "d@e.com", Subject: "s"},
			wantCT:    "Content-Type: text/plain; charset=UTF-8",
			wantBody:  "",
			notWantCT: "multipart/alternative",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cap capturedSend
			m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
			if err := m.Send(context.Background(), tt.msg); err != nil {
				t.Fatalf("Send: %v", err)
			}
			out := string(cap.msg)
			mustContain(t, out, tt.wantCT)
			if tt.wantBody != "" {
				mustContain(t, out, tt.wantBody)
			}
			if strings.Contains(out, tt.notWantCT) {
				t.Fatalf("did not expect %q in:\n%s", tt.notWantCT, out)
			}
		})
	}
}

func TestSMTPMailerEscapesMaliciousOrgName(t *testing.T) {
	var cap capturedSend
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	// An invitation whose org name is an XSS attempt.
	inv := InvitationEmail("Alice", `<script>alert('x')</script>`, "", "https://vortex.dev/accept?t=abc")
	inv.To = "victim@example.com"
	if err := m.Send(context.Background(), inv); err != nil {
		t.Fatalf("Send: %v", err)
	}
	out := string(cap.msg)
	html := htmlPart(t, out)
	if strings.Contains(html, "<script>alert('x')</script>") {
		t.Fatalf("raw <script> leaked into HTML part:\n%s", html)
	}
	mustContain(t, html, "&lt;script&gt;")
}

func TestSMTPMailerHeaderInjectionStripped(t *testing.T) {
	var cap capturedSend
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	msg := Message{
		To:       "d@e.com",
		Subject:  "Hi\r\nBcc: evil@example.com",
		TextBody: "body",
	}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	out := string(cap.msg)
	if strings.Contains(out, "\r\nBcc: evil@example.com") {
		t.Fatalf("subject CRLF injection not stripped:\n%s", out)
	}
	mustContain(t, out, "Subject: HiBcc: evil@example.com\r\n")
}

func TestSMTPMailerNonASCIISubjectEncoded(t *testing.T) {
	var cap capturedSend
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	if err := m.Send(context.Background(), Message{To: "d@e.com", Subject: "Café ☕", TextBody: "b"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	out := string(cap.msg)
	mustContain(t, out, "Subject: =?UTF-8?B?")
	if strings.Contains(out, "Subject: Café") {
		t.Fatalf("non-ASCII subject was not encoded")
	}
}

func TestSMTPMailerEmptyRecipientRejected(t *testing.T) {
	var cap capturedSend
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	err := m.Send(context.Background(), Message{To: "  ", TextBody: "x"})
	if err == nil {
		t.Fatalf("expected error for empty recipient")
	}
	if cap.msg != nil {
		t.Fatalf("transport should not be called for invalid message")
	}
}

func TestSMTPMailerContextCancelled(t *testing.T) {
	var cap capturedSend
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Send(ctx, Message{To: "d@e.com", TextBody: "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if cap.msg != nil {
		t.Fatalf("transport should not run with cancelled context")
	}
}

func TestSMTPMailerPropagatesTransportError(t *testing.T) {
	var cap capturedSend
	cap.err = errors.New("smtp down")
	m := newSMTPMailerWithCapture(Config{Host: "h", Port: 1, From: "f@x.com"}, &cap)
	err := m.Send(context.Background(), Message{To: "d@e.com", TextBody: "x"})
	if err == nil || !strings.Contains(err.Error(), "smtp down") {
		t.Fatalf("err = %v, want transport error", err)
	}
}

func TestNewSMTPMailerDefaultsToSendMail(t *testing.T) {
	m := NewSMTPMailer(Config{Host: "h", Port: 1})
	if m.send == nil {
		t.Fatalf("default send func should be set")
	}
	if m.now == nil {
		t.Fatalf("default clock should be set")
	}
}

func TestDialTLSConfig(t *testing.T) {
	m := NewSMTPMailer(Config{Host: "smtp.example.com", Port: 587, StartTLS: true})
	cfg := m.dialTLSConfig()
	if cfg.ServerName != "smtp.example.com" {
		t.Fatalf("ServerName = %q", cfg.ServerName)
	}
	if cfg.MinVersion == 0 {
		t.Fatalf("expected a minimum TLS version")
	}
}

func TestSMTPMailerNilSendDefaultsToSendMail(t *testing.T) {
	// With a nil send func and nil clock, Send must fall back to defaults. We
	// point the mailer at an unreachable address so smtp.SendMail returns an
	// error quickly rather than succeeding; we only care that the fallback path
	// is exercised (no panic) and an error is surfaced.
	m := NewSMTPMailer(Config{Host: "127.0.0.1", Port: 1, From: "f@x.com"})
	m.send = nil
	m.now = nil
	err := m.Send(context.Background(), Message{To: "d@e.com", TextBody: "x"})
	if err == nil {
		t.Fatalf("expected dial error from default smtp.SendMail")
	}
}

func TestComposeMessageDefaultClockProducesDate(t *testing.T) {
	// Exercise composeMessage with a real timestamp to cover the clock default.
	m := NewSMTPMailer(Config{Host: "h", Port: 1, From: "f@x.com"})
	var got []byte
	m.send = func(_ string, _ smtp.Auth, _ string, _ []string, msg []byte) error {
		got = msg
		return nil
	}
	if err := m.Send(context.Background(), Message{To: "d@e.com", TextBody: "x"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	mustContain(t, string(got), "Date: ")
}

func TestBase64StdEncodings(t *testing.T) {
	// Cover all remainder cases: 1, 2 and 3 trailing bytes.
	cases := map[string]string{
		"":     "",
		"f":    "Zg==",
		"fo":   "Zm8=",
		"foo":  "Zm9v",
		"foob": "Zm9vYg==",
		"€":    "4oKs", // 3-byte UTF-8 sequence
	}
	for in, want := range cases {
		if got := base64Std(in); got != want {
			t.Fatalf("base64Std(%q) = %q, want %q", in, got, want)
		}
	}
}

// htmlPart returns the text/html section of a composed multipart message.
func htmlPart(t *testing.T, msg string) string {
	t.Helper()
	idx := strings.Index(msg, "text/html")
	if idx < 0 {
		t.Fatalf("no text/html part found in:\n%s", msg)
	}
	return msg[idx:]
}

// mustContain fails the test if haystack does not contain needle.
func mustContain(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected to find %q in:\n%s", needle, haystack)
	}
}

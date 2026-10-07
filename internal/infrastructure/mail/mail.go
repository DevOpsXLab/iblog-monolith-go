// Package mail sends email over SMTP (Mailpit in development).
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"sync"
	"time"

	"github.com/DevOpsXLab/iblog-monolith-go/internal/application"
	"github.com/DevOpsXLab/iblog-monolith-go/internal/infrastructure/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type SMTP struct {
	Addr string // host:port
	From string
}

func (s SMTP) Send(ctx context.Context, m application.Email) (err error) {
	_, span := telemetry.Start(ctx, "smtp.send", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("server.address", s.Addr)))
	defer func() { telemetry.End(span, err) }()
	var h strings.Builder
	fmt.Fprintf(&h, "From: %s\r\nTo: %s\r\nSubject: %s\r\n", header(s.From), header(m.To), mime.QEncoding.Encode("utf-8", header(m.Subject)))
	if m.Unsubscribe != "" {
		fmt.Fprintf(&h, "List-Unsubscribe: <%s>\r\nList-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n", header(m.Unsubscribe))
	}
	msg := h.String() + "Content-Type: text/plain; charset=utf-8\r\n\r\n" + strings.ReplaceAll(m.Text, "\n", "\r\n")
	return s.send(ctx, m.To, []byte(msg))
}

// sendTimeout bounds one whole SMTP exchange so a stalled server cannot pin
// a worker goroutine (smtp.SendMail has no timeout at all).
const sendTimeout = 30 * time.Second

func (s SMTP) send(ctx context.Context, to string, msg []byte) error {
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.Addr)
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	// Abort blocked I/O if ctx is cancelled before the deadline.
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()
	host, _, _ := net.SplitHostPort(s.Addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := wc.Write(msg); err != nil {
		wc.Close()
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// header drops line breaks, so values (post titles in subjects) cannot
// inject headers.
func header(v string) string {
	return strings.Join(strings.FieldsFunc(v, func(r rune) bool { return r == '\r' || r == '\n' }), " ")
}

// Memory keeps sent mail in memory (tests, or when SMTP is not configured).
type Memory struct {
	mu   sync.Mutex
	sent []application.Email
}

func (m *Memory) Send(_ context.Context, e application.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, e)
	return nil
}

func (m *Memory) Sent() []application.Email {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]application.Email(nil), m.sent...)
}

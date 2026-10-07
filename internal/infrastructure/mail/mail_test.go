package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/iBlog/iblog-monolith-go/internal/application"
)

// fakeSMTP accepts one connection, speaks just enough SMTP for net/smtp and
// returns the DATA payload.
func fakeSMTP(t *testing.T) (addr string, data <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		say := func(s string) { c.Write([]byte(s + "\r\n")) }
		say("220 fake")
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				say("250 fake")
			case strings.HasPrefix(cmd, "DATA"):
				say("354 go")
				var b strings.Builder
				for {
					l, err := r.ReadString('\n')
					if err != nil {
						return
					}
					if l == ".\r\n" {
						break
					}
					b.WriteString(l)
				}
				out <- b.String()
				say("250 ok")
			case strings.HasPrefix(cmd, "QUIT"):
				say("221 bye")
				return
			default:
				say("250 ok")
			}
		}
	}()
	return ln.Addr().String(), out
}

func TestSendDeliversAndStripsHeaderInjection(t *testing.T) {
	addr, data := fakeSMTP(t)
	s := SMTP{Addr: addr, From: "blog@example.com"}
	err := s.Send(context.Background(), application.Email{
		To:          "a@example.com",
		Subject:     "Hi\r\nBcc: evil@example.com",
		Text:        "line1\nline2",
		Unsubscribe: "https://example.com/u",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := <-data
	if strings.Contains(got, "\r\nBcc:") {
		t.Errorf("header injected:\n%s", got)
	}
	for _, want := range []string{"To: a@example.com\r\n", "List-Unsubscribe: <https://example.com/u>\r\n", "line1\r\nline2"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestSendStalledServerHonoursContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { // accept, then never send the greeting
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(5 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = SMTP{Addr: ln.Addr().String(), From: "x@example.com"}.Send(ctx, application.Email{To: "a@example.com"})
	if err == nil {
		t.Fatal("expected error from stalled server")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("send blocked for %s", d)
	}
}

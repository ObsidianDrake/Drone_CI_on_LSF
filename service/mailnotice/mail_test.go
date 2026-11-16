package mailnotice

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/shared/db/dbtest"
)

type sink struct{ calls int }

func (s *sink) Send(context.Context, *core.WebhookData) error { s.calls++; return nil }

func TestMessage(t *testing.T) {
	c := Config{From: "drone@example.com", BaseURL: "https://example.com/drone"}
	r := &core.Repository{Namespace: "org", Name: "repo", Slug: "org/repo"}
	b := &core.Build{Number: 42, AuthorEmail: "author@example.com", Status: core.StatusPassing, Started: 10, Finished: 70}
	from, _, rcpt, data, err := message(c, r, b)
	if err != nil {
		t.Fatal(err)
	}
	if from != c.From || len(rcpt) != 1 || rcpt[0] != b.AuthorEmail {
		t.Fatalf("unexpected recipients: %v", rcpt)
	}
	for _, v := range []string{"https://example.com/drone/org/repo/42", "Duration: 1m0s", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(string(data), v) {
			t.Fatalf("missing %s", v)
		}
	}
	r.MailTo = "one@example.com, two@example.com"
	r.MailCC = "cc@example.com, one@example.com"
	from, _, rcpt, data, err = message(c, r, b)
	if err != nil || from != c.From || len(rcpt) != 3 || !strings.Contains(string(data), "Cc:") {
		t.Fatalf("override failed: %v %v", rcpt, err)
	}
	r.MailTo = ""
	b.AuthorEmail = ""
	if _, _, _, _, err = message(c, r, b); err == nil {
		t.Fatal("missing author accepted")
	}
}
func TestValidate(t *testing.T) {
	for _, v := range []string{"bad", "x@example.com\r\nBcc: y@example.com", strings.Repeat("a", 2049)} {
		if Validate(v, false) == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	if Validate("a@example.com,b@example.com", true) == nil {
		t.Fatal("multiple senders accepted")
	}
	if Validate("", true) != nil || Validate("a@example.com,b@example.com", false) != nil {
		t.Fatal("valid settings rejected")
	}
}
func TestCompletionDelivery(t *testing.T) {
	database, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer dbtest.Disconnect(database)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		io.WriteString(conn, "220 test SMTP\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "DATA"):
				io.WriteString(conn, "354 send data\r\n")
				var body strings.Builder
				for {
					line, err = reader.ReadString('\n')
					if err != nil {
						return
					}
					if line == ".\r\n" {
						break
					}
					body.WriteString(line)
				}
				received <- body.String()
				io.WriteString(conn, "250 accepted\r\n")
			case strings.HasPrefix(line, "QUIT"):
				io.WriteString(conn, "221 bye\r\n")
				return
			default:
				io.WriteString(conn, "250 OK\r\n")
			}
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	next := &sink{}
	s := New(next, database, Config{Host: host, Port: port, From: "sender@example.com", BaseURL: "https://drone.example.com"})
	in := &core.WebhookData{Event: core.WebhookEventBuild, Action: core.WebhookActionUpdated, Repo: &core.Repository{MailNotice: true}, Build: &core.Build{ID: 12345, AuthorEmail: "author@example.com", Status: core.StatusRunning}}
	if err = s.Send(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.Build.Status = core.StatusPassing
	in.Build.Finished = 10
	if err = s.Send(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no email")
	}
	// A fresh sender uses the durable claim instead of sending twice.
	s = New(next, database, Config{Host: host, Port: port, From: "sender@example.com"})
	if err = s.Send(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if next.calls != 3 {
		t.Fatal("existing webhook was not preserved")
	}
	in.Build.ID++
	in.Repo.MailTo = "invalid"
	if err = s.Send(context.Background(), in); err != nil {
		t.Fatal("mail failure changed build result")
	}
}

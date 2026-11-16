// Package mailnotice sends repository-level build completion notifications.
package mailnotice

import (
	"context"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/shared/db"
	"github.com/sirupsen/logrus"
)

type Config struct{ Host, Port, From, BaseURL string }
type sender struct {
	next   core.WebhookSender
	db     *db.DB
	config Config
}

func New(next core.WebhookSender, database *db.DB, config Config) core.WebhookSender {
	return &sender{next: next, db: database, config: config}
}

// Validate rejects header injection and accepts comma-separated mailbox lists.
func Validate(value string, single bool) error {
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("invalid email address list")
	}
	if strings.TrimSpace(value) == "" {
		return nil
	}
	a, err := mail.ParseAddressList(value)
	if err != nil || len(a) == 0 {
		return fmt.Errorf("use valid email addresses separated by commas")
	}
	if single && len(a) != 1 {
		return fmt.Errorf("specify exactly one sender")
	}
	for _, v := range a {
		if !strings.Contains(v.Address, "@") {
			return fmt.Errorf("email address must include a domain")
		}
	}
	return nil
}
func terminal(status string) bool {
	switch status {
	case core.StatusPassing, core.StatusFailing, core.StatusError, core.StatusKilled:
		return true
	}
	return false
}
func (s *sender) Send(ctx context.Context, in *core.WebhookData) error {
	// Mail delivery failures must never change the webhook or build result.
	if in != nil && in.Event == core.WebhookEventBuild && in.Action != core.WebhookActionDeleted && in.Build != nil && in.Repo != nil && in.Repo.MailNotice && terminal(in.Build.Status) && in.Build.Finished > 0 {
		if err := s.notify(in.Repo, in.Build); err != nil {
			logrus.WithField("build.id", in.Build.ID).WithError(err).Warn("mail notice: delivery failed or skipped")
		}
	}
	return s.next.Send(ctx, in)
}
func (s *sender) notify(repo *core.Repository, build *core.Build) error {
	from, _, recipients, data, err := message(s.config, repo, build)
	if err != nil {
		return err
	}
	if s.config.Host == "" {
		return fmt.Errorf("DRONE_SMTP_HOST is not configured")
	}
	// A durable claim prevents duplicate completion events (including after restart).
	// Claim before SMTP: an ambiguous SMTP response must not cause duplicate mail.
	claimed := false
	err = s.db.Update(func(e db.Execer, b db.Binder) error {
		q, args, err := b.BindNamed("SELECT COUNT(*) FROM mail_notices WHERE build_id = :id", map[string]interface{}{"id": build.ID})
		if err != nil {
			return err
		}
		var count int
		if err = e.QueryRow(q, args...).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return nil
		}
		q, args, err = b.BindNamed("INSERT INTO mail_notices (build_id) VALUES (:id)", map[string]interface{}{"id": build.ID})
		if err != nil {
			return err
		}
		_, err = e.Exec(q, args...)
		claimed = err == nil
		return err
	})
	if err != nil || !claimed {
		return err
	}
	return deliver(s.config, from, recipients, data)
}
func message(c Config, r *core.Repository, b *core.Build) (string, string, []string, []byte, error) {
	from, to, cc := strings.TrimSpace(c.From), strings.TrimSpace(r.MailTo), strings.TrimSpace(r.MailCC)
	if to == "" {
		to = b.AuthorEmail
	}
	for _, v := range []struct {
		value  string
		single bool
	}{{from, true}, {to, false}, {cc, false}} {
		if err := Validate(v.value, v.single); err != nil {
			return "", "", nil, nil, err
		}
	}
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return "", "", nil, nil, fmt.Errorf("sender or recipient email is missing")
	}
	fa, _ := mail.ParseAddress(from)
	ta, _ := mail.ParseAddressList(to)
	ca, _ := mail.ParseAddressList(cc)
	recipients := []string{}
	seen := map[string]bool{}
	for _, a := range append(ta, ca...) {
		if !seen[a.Address] {
			recipients = append(recipients, a.Address)
			seen[a.Address] = true
		}
	}
	format := func(list []*mail.Address) string {
		v := []string{}
		for _, a := range list {
			v = append(v, a.String())
		}
		return strings.Join(v, ", ")
	}
	clean := func(v string) string { return strings.NewReplacer("\r", " ", "\n", " ").Replace(v) }
	subject := fmt.Sprintf("[Drone] %s - %s #%d", strings.ToUpper(b.Status), clean(r.Slug), b.Number)
	link := strings.TrimRight(c.BaseURL, "/") + "/" + url.PathEscape(r.Namespace) + "/" + url.PathEscape(r.Name) + fmt.Sprintf("/%d", b.Number)
	duration := "Not started"
	if b.Started > 0 && b.Finished >= b.Started {
		duration = (time.Duration(b.Finished-b.Started) * time.Second).String()
	}
	body := fmt.Sprintf("Repository: %s\nBuild: #%d\nBranch: %s\nStatus: %s\nCommit: %s\nCommit author: %s\nDuration: %s\n\nView build:\n%s\n", r.Slug, b.Number, b.Target, b.Status, b.After, b.AuthorEmail, duration, link)
	headers := "From: " + fa.String() + "\r\nTo: " + format(ta) + "\r\n"
	if len(ca) > 0 {
		headers += "Cc: " + format(ca) + "\r\n"
	}
	headers += "Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\nDate: " + time.Now().Format(time.RFC1123Z) + "\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n"
	return fa.Address, to, recipients, []byte(headers + strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")), nil
}
func deliver(c Config, from string, recipients []string, data []byte) error {
	port := c.Port
	if port == "" {
		port = "25"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.Host, port), 30*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return err
	}
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		return err
	}
	defer client.Close()
	// Deliberately no authentication or STARTTLS: internal SMTP relay.
	if err = client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err = client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err = writer.Write(data); err != nil {
		return err
	}
	if err = writer.Close(); err != nil {
		return err
	}
	// DATA acceptance is success even if the server closes before QUIT.
	_ = client.Quit()
	return nil
}

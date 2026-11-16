package repos

import (
	"context"
	"testing"
)

func TestMailNoticeSettings(t *testing.T) {
	_, s, repo := numberFixture(t)
	for _, body := range []string{`{"mail_to":"a@example.com\r\nBcc: b@example.com"}`, `{"mail_cc":"bad"}`} {
		if w := numberRequest(s, body, false); w.Code != 400 {
			t.Fatalf("invalid mail setting accepted: %d %s", w.Code, w.Body.String())
		}
	}
	w := numberRequest(s, `{"mail_notice":true,"mail_to":"one@example.com, two@example.com","mail_cc":"cc@example.com"}`, false)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	r, err := s.Find(context.Background(), repo.ID)
	if err != nil || !r.MailNotice || r.MailCC != "cc@example.com" || r.MailTo != "one@example.com, two@example.com" {
		t.Fatalf("not persisted: %+v %v", r, err)
	}
	w = numberRequest(s, `{"mail_to":"","mail_cc":""}`, false)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	r, err = s.Find(context.Background(), repo.ID)
	if err != nil || r.MailTo != "" || r.MailCC != "" {
		t.Fatal("cannot restore defaults")
	}
}

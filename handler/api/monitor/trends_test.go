package monitor

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/monitor"
)

type fakeReader struct{ selected []monitor.Repository }

func (*fakeReader) Repositories(context.Context) ([]monitor.Repository, error) {
	return []monitor.Repository{{ID: 1, Slug: "public/a", Visibility: "public"}, {ID: 2, Slug: "internal/b", Visibility: "internal"}, {ID: 3, Slug: "private/allowed", Visibility: "private"}, {ID: 4, Slug: "secret/forbidden", Visibility: "private"}}, nil
}
func (f *fakeReader) Read(_ context.Context, repos []monitor.Repository, _ time.Time, _ int) (*monitor.Result, error) {
	f.selected = repos
	return &monitor.Result{Series: []monitor.Series{}}, nil
}

type fakeRemote struct {
	core.RepositoryService
	allow bool
	err   error
}

func (f *fakeRemote) FindPerm(_ context.Context, _ *core.User, slug string) (*core.Perm, error) {
	return &core.Perm{Read: f.allow && slug == "private/allowed"}, f.err
}
func TestVisibilityAndRevocation(t *testing.T) {
	store := &fakeReader{}
	remote := &fakeRemote{allow: true}
	for _, tc := range []struct {
		admin, allow bool
		want         int
	}{{false, true, 3}, {false, false, 2}, {true, false, 4}} {
		remote.allow = tc.allow
		r := httptest.NewRequest("GET", "/?repos=1,2,3,4&hours=168", nil)
		r = r.WithContext(request.WithUser(r.Context(), &core.User{ID: 1, Active: true, Admin: tc.admin}))
		w := httptest.NewRecorder()
		Handle(store, remote, true)(w, r)
		if w.Code != 200 || len(store.selected) != tc.want {
			t.Fatalf("status %d selection %v", w.Code, store.selected)
		}
		if !tc.admin && strings.Contains(w.Body.String(), "secret/forbidden") {
			t.Fatal("private repo leaked")
		}
		if !tc.admin && !tc.allow && strings.Contains(w.Body.String(), "private/allowed") {
			t.Fatal("revoked repo leaked")
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("permission-sensitive data cacheable")
		}
	}
	remote.err = errors.New("SCM offline")
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(request.WithUser(r.Context(), &core.User{Active: true}))
	w := httptest.NewRecorder()
	Handle(store, remote, true)(w, r)
	if len(store.selected) != 2 || strings.Contains(w.Body.String(), "private/") {
		t.Fatal("SCM failure did not fail closed")
	}
}
func TestInputAndAuthentication(t *testing.T) {
	for _, tc := range []struct {
		query  string
		user   *core.User
		status int
	}{
		{"", nil, 401}, {"", &core.User{}, 403}, {"?hours=169", &core.User{Active: true}, 400}, {"?hours=-1", &core.User{Active: true}, 400}, {"?repos=0", &core.User{Active: true}, 400}, {"?repos=1,2,3,4,5,6,7,8,9,10,11,12,13", &core.User{Active: true}, 400},
	} {
		r := httptest.NewRequest("GET", "/"+tc.query, nil)
		if tc.user != nil {
			r = r.WithContext(request.WithUser(r.Context(), tc.user))
		}
		w := httptest.NewRecorder()
		Handle(&fakeReader{}, &fakeRemote{}, true)(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s got %d", tc.query, w.Code)
		}
	}
}

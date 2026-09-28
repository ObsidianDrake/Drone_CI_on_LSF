package builds

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/request"
	"github.com/go-chi/chi"
)

type historyRepos struct{ core.RepositoryStore }

func (historyRepos) FindName(context.Context, string, string) (*core.Repository, error) {
	return &core.Repository{ID: 42}, nil
}

type historyBuilds struct {
	core.BuildStore
	calls  int
	err    error
	filter core.HistoryFilter
	token  string
}

func (s *historyBuilds) DeleteHistory(_ context.Context, repo int64, f core.HistoryFilter, token string) (*core.HistoryPreview, error) {
	s.calls++
	s.filter = f
	s.token = token
	if repo != 42 {
		panic("wrong repository")
	}
	return &core.HistoryPreview{Numbers: []int64{1}, Token: "snapshot", StepIDs: []int64{12}}, s.err
}

type historyLogs struct {
	core.LogStore
	calls int
	err   error
}

func (s *historyLogs) Delete(context.Context, int64) error { s.calls++; return s.err }

func TestHistoryAPI(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		user        *core.User
		storeErr    error
		code        int
		calls, logs int
	}{
		{name: "anonymous", body: `{"numbers":[1]}`, code: 403},
		{name: "repository admin is insufficient", body: `{"numbers":[1]}`, user: &core.User{}, code: 403},
		{name: "preview", body: `{"numbers":[2,1]}`, code: 200, calls: 1},
		{name: "commit", body: `{"numbers":[1],"commit":true,"token":"snapshot"}`, code: 200, calls: 1, logs: 1},
		{name: "before", body: `{"before":12}`, code: 200, calls: 1},
		{name: "changed", body: `{"before":12,"commit":true,"token":"old"}`, storeErr: core.ErrHistoryChanged, code: 409, calls: 1},
		{name: "store error", body: `{"before":12}`, storeErr: errors.New("database unavailable"), code: 500, calls: 1},
		{name: "empty", body: `{}`, code: 400},
		{name: "ambiguous", body: `{"before":2,"numbers":[1]}`, code: 400},
		{name: "negative", body: `{"before":-1}`, code: 400},
		{name: "decimal", body: `{"before":1.5}`, code: 400},
		{name: "duplicate", body: `{"numbers":[1,1]}`, code: 400},
		{name: "invalid number", body: `{"numbers":[0]}`, code: 400},
		{name: "unconfirmed", body: `{"before":2,"commit":true}`, code: 400},
		{name: "token without commit", body: `{"before":2,"token":"x"}`, code: 400},
		{name: "unknown", body: `{"before":2,"force":true}`, code: 400},
		{name: "extra JSON", body: `{"before":2}{}`, code: 400},
		{name: "non-success global", body: `{"non_success":true}`, code: 200, calls: 1},
		{name: "non-success global commit", body: `{"non_success":true,"commit":true,"token":"snapshot"}`, code: 200, calls: 1, logs: 1},
		{name: "ambiguous non-success", body: `{"before":2,"non_success":true}`, code: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := tc.user
			if user == nil && tc.name != "anonymous" {
				user = &core.User{Admin: true}
			}
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			ctx := r.Context()
			if user != nil {
				ctx = request.WithUser(ctx, user)
			}
			route := chi.NewRouteContext()
			route.URLParams.Add("owner", "test")
			route.URLParams.Add("name", "repo")
			r = r.WithContext(context.WithValue(ctx, chi.RouteCtxKey, route))
			s := &historyBuilds{err: tc.storeErr}
			logs := &historyLogs{}
			w := httptest.NewRecorder()
			HandleHistory(historyRepos{}, s, logs)(w, r)
			if w.Code != tc.code {
				t.Fatalf("status %d want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if s.calls != tc.calls || logs.calls != tc.logs {
				t.Fatalf("store calls %d, log calls %d", s.calls, logs.calls)
			}
			if tc.name == "preview" && (s.filter.Numbers[0] != 1 || s.token != "") {
				t.Fatal("invalid preview forwarding")
			}
		})
	}
}

func TestHistoryCleanupFailure(t *testing.T) {
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"numbers":[1],"commit":true,"token":"snapshot"}`))
	r = r.WithContext(context.WithValue(request.WithUser(r.Context(), &core.User{Admin: true}), chi.RouteCtxKey, chi.NewRouteContext()))
	w := httptest.NewRecorder()
	HandleHistory(historyRepos{}, &historyBuilds{}, &historyLogs{err: errors.New("offline")})(w, r)
	var result struct {
		CleanupFailures int `json:"cleanup_failures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.CleanupFailures != 1 {
		t.Fatalf("cleanup failure hidden: %s", w.Body.String())
	}
}

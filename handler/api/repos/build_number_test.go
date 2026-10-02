package repos

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/store/build"
	repostore "github.com/drone/drone/store/repos"
	"github.com/drone/drone/store/shared/db"
	"github.com/drone/drone/store/shared/db/dbtest"
	"github.com/go-chi/chi"
)

func numberFixture(t *testing.T) (*db.DB, core.RepositoryStore, *core.Repository) {
	t.Helper()
	d, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	s := repostore.New(d)
	repo := &core.Repository{UID: "migration", Namespace: "org", Name: "qc", Slug: "org/qc", Active: true, Timeout: 60}
	if err := s.Create(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	return d, s, repo
}

func numberRequest(s core.RepositoryStore, body string, admin bool) *httptest.ResponseRecorder {
	c := chi.NewRouteContext()
	c.URLParams.Add("owner", "org")
	c.URLParams.Add("name", "qc")
	r := httptest.NewRequest("PATCH", "/", strings.NewReader(body))
	r = r.WithContext(request.WithUser(context.WithValue(r.Context(), chi.RouteCtxKey, c), &core.User{Admin: admin}))
	w := httptest.NewRecorder()
	HandleUpdate(s)(w, r)
	return w
}

func TestNextBuildNumberMigration(t *testing.T) {
	d, s, repo := numberFixture(t)
	ctx := context.Background()
	// The UI receives actual eligibility, independently of the counter.
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(request.WithRepo(r.Context(), repo))
	w := httptest.NewRecorder()
	HandleFind(s)(w, r)
	var shown core.Repository
	if err := json.Unmarshal(w.Body.Bytes(), &shown); err != nil || !shown.NextBuildNumberEditable {
		t.Fatalf("eligibility: %s, %v", w.Body.String(), err)
	}
	w = numberRequest(s, `{"next_build_number":5001,"timeout_hours":2}`, true)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	var saved core.Repository
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil || saved.Counter != 5000 || saved.Timeout != 120 || !saved.NextBuildNumberEditable {
		t.Fatalf("saved: %s, %v", w.Body.String(), err)
	}
	// A trigger holding the old repository snapshot must retry and use 5001.
	first, err := s.Increment(ctx, repo)
	if err != nil || first.Counter != 5001 {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err := build.New(d).Create(ctx, &core.Build{RepoID: repo.ID, Number: first.Counter, Status: core.StatusPending}, nil); err != nil {
		t.Fatal(err)
	}
	w = numberRequest(s, `{"next_build_number":6001,"timeout_hours":3}`, true)
	if w.Code != 409 {
		t.Fatalf("existing build accepted: %d %s", w.Code, w.Body.String())
	}
	current, err := s.Find(ctx, repo.ID)
	if err != nil || current.Counter != 5001 || current.Timeout != 120 {
		t.Fatalf("failed save partially applied: %+v %v", current, err)
	}
	editable, err := s.(core.RepositoryBuildNumberStore).CanSetNextBuildNumber(ctx, repo.ID)
	if err != nil || editable {
		t.Fatalf("existing build editable: %v %v", editable, err)
	}
	second, err := s.Increment(ctx, current)
	if err != nil || second.Counter != 5002 {
		t.Fatalf("second: %+v %v", second, err)
	}
}

func TestNextBuildNumberValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		admin, inactive bool
		code            int
	}{
		{"one", `{"next_build_number":1}`, true, false, 200},
		{"upper bound", `{"next_build_number":2147483647}`, true, false, 200},
		{"zero", `{"next_build_number":0}`, true, false, 400},
		{"negative", `{"next_build_number":-1}`, true, false, 400},
		{"fraction", `{"next_build_number":1.5}`, true, false, 400},
		{"string", `{"next_build_number":"5001"}`, true, false, 400},
		{"null", `{"next_build_number":null}`, true, false, 400},
		{"overflow", `{"next_build_number":2147483648}`, true, false, 400},
		{"conflict", `{"next_build_number":10,"counter":9}`, true, false, 400},
		{"nonadmin", `{"next_build_number":10}`, false, false, 403},
		{"inactive", `{"next_build_number":10}`, true, true, 409},
		{"legacy", `{"counter":99}`, true, false, 200},
		{"legacy nonadmin", `{"counter":99}`, false, false, 403},
		{"legacy negative", `{"counter":-1}`, true, false, 400},
		{"legacy overflow", `{"counter":9223372036854775807}`, true, false, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, s, repo := numberFixture(t)
			if tc.inactive {
				repo.Active = false
				if err := s.Update(context.Background(), repo); err != nil {
					t.Fatal(err)
				}
			}
			w := numberRequest(s, tc.body, tc.admin)
			if w.Code != tc.code {
				t.Fatalf("got %d want %d: %s", w.Code, tc.code, w.Body.String())
			}
			if tc.code != 200 {
				current, err := s.Find(context.Background(), repo.ID)
				if err != nil || current.Counter != 0 {
					t.Fatalf("rejected input changed counter: %+v %v", current, err)
				}
			}
		})
	}
}

func TestNextBuildNumberAllHistoryBlocksChanges(t *testing.T) {
	for _, status := range []string{"pending", "running", "success", "failure", "killed", "blocked", "declined", "error", "skipped"} {
		t.Run(status, func(t *testing.T) {
			d, s, repo := numberFixture(t)
			// Even imported records with counter=0 must block both APIs.
			if err := build.New(d).Create(context.Background(), &core.Build{RepoID: repo.ID, Number: 42, Status: status}, nil); err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{`{"next_build_number":5001}`, `{"counter":5000}`} {
				w := numberRequest(s, body, true)
				if w.Code != 409 {
					t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
				}
			}
		})
	}
}

func TestNextBuildNumberResetAfterDeletingAllRecords(t *testing.T) {
	d, s, repo := numberFixture(t)
	ctx := context.Background()
	builds := build.New(d)
	sequence := builds.(core.BuildSequenceStore)
	w := numberRequest(s, `{"next_build_number":5001}`, true)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	first := &core.Build{RepoID: repo.ID, Status: core.StatusPassing}
	if err := sequence.CreateNext(ctx, repo, first, nil); err != nil || first.Number != 5001 {
		t.Fatalf("first: %+v %v", first, err)
	}
	second := &core.Build{RepoID: repo.ID, Status: core.StatusFailing}
	if err := sequence.CreateNext(ctx, repo, second, nil); err != nil || second.Number != 5002 {
		t.Fatalf("second: %+v %v", second, err)
	}
	if err := builds.Delete(ctx, first); err != nil {
		t.Fatal(err)
	}
	if w := numberRequest(s, `{"next_build_number":1}`, true); w.Code != 409 {
		t.Fatalf("partial deletion: %d", w.Code)
	}
	if err := builds.Delete(ctx, second); err != nil {
		t.Fatal(err)
	}
	editable, err := s.(core.RepositoryBuildNumberStore).CanSetNextBuildNumber(ctx, repo.ID)
	if err != nil || !editable {
		t.Fatalf("empty repo not editable: %v %v", editable, err)
	}
	for _, number := range []string{"100", "1"} {
		w := numberRequest(s, `{"next_build_number":`+number+`}`, true)
		if w.Code != 200 {
			t.Fatalf("reset: %d %s", w.Code, w.Body.String())
		}
	}
	reset := &core.Build{RepoID: repo.ID, Status: core.StatusPending}
	if err := sequence.CreateNext(ctx, repo, reset, nil); err != nil || reset.Number != 1 {
		t.Fatalf("reset build: %+v %v", reset, err)
	}
}

func TestNextBuildNumberAtomicTriggerAndSettings(t *testing.T) {
	d, s, repo := numberFixture(t)
	ctx := context.Background()
	guard := s.(core.RepositoryBuildNumberStore)
	stale := *repo
	sequence := build.New(d).(core.BuildSequenceStore)
	first := &core.Build{RepoID: repo.ID, Status: core.StatusPending}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var setErr, buildErr error
	wg.Add(2)
	go func() { defer wg.Done(); <-start; setErr = guard.UpdateNextBuildNumber(ctx, &stale, 5001) }()
	go func() { defer wg.Done(); <-start; buildErr = sequence.CreateNext(ctx, repo, first, nil) }()
	close(start)
	wg.Wait()
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	if setErr == nil {
		if first.Number != 5001 {
			t.Fatalf("saved number not used: %d", first.Number)
		}
	} else if setErr != db.ErrOptimisticLock || first.Number != 1 {
		t.Fatalf("conflict: %v number %d", setErr, first.Number)
	}
	// Once allocation commits there is already a record: no unrecorded-number gap.
	current, err := s.Find(ctx, repo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.UpdateNextBuildNumber(ctx, current, 10); err != db.ErrOptimisticLock {
		t.Fatalf("existing record: %v", err)
	}
}

package monitor

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/build"
	"github.com/drone/drone/store/repos"
	"github.com/drone/drone/store/shared/db"
	"github.com/drone/drone/store/shared/db/dbtest"
)

func fixture(t *testing.T) *Store {
	t.Helper()
	database, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return &Store{DB: database}
}

func TestRepositoriesOnlyActive(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	repositories := repos.New(s.DB)
	idle := &core.Repository{UID: "idle", Slug: "org/z-idle", Active: true, Visibility: core.VisibilityPrivate}
	busy := &core.Repository{UID: "busy", Slug: "org/a-busy", Active: true, Visibility: core.VisibilityPublic}
	inactive := &core.Repository{UID: "inactive", Slug: "org/inactive", Visibility: core.VisibilityInternal}
	for _, repo := range []*core.Repository{idle, inactive, busy} {
		if err := repositories.Create(ctx, repo); err != nil {
			t.Fatal(err)
		}
	}
	addBuild(t, s, busy.ID, 1, core.StatusRunning)
	addBuild(t, s, inactive.ID, 1, core.StatusRunning)
	check := func(want ...*core.Repository) {
		t.Helper()
		got, err := s.Repositories(ctx)
		if err != nil {
			t.Fatal(err)
		}
		expected := []Repository{}
		for _, repo := range want {
			expected = append(expected, Repository{ID: repo.ID, Slug: repo.Slug, Created: repo.Created, Visibility: repo.Visibility})
		}
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("repositories = %#v, want %#v", got, expected)
		}
	}
	// Activation, not running builds or visibility, determines monitor membership.
	check(busy, idle)
	for _, repo := range []*core.Repository{busy, idle} {
		repo.Active = false
		if err := repositories.Update(ctx, repo); err != nil {
			t.Fatal(err)
		}
	}
	check()
	inactive.Active = true
	if err := repositories.Update(ctx, inactive); err != nil {
		t.Fatal(err)
	}
	check(inactive)
}

func addBuild(t *testing.T, s *Store, repo, number int64, status string) {
	t.Helper()
	if err := build.New(s.DB).Create(context.Background(), &core.Build{RepoID: repo, Number: number, Status: status}, nil); err != nil {
		t.Fatal(err)
	}
}
func val(t *testing.T, v *int, want int) {
	t.Helper()
	if v == nil || *v != want {
		t.Fatalf("value %v want %d", v, want)
	}
}
func TestSampleRecoveryAndGaps(t *testing.T) {
	ctx := context.Background()
	s := fixture(t)
	now := time.Unix(1800000000, 0)
	addBuild(t, s, 1, 1, "running")
	addBuild(t, s, 1, 2, "pending")
	addBuild(t, s, 1, 3, "blocked")
	addBuild(t, s, 1, 4, "success")
	original := &Service{Store: s, Enabled: true}
	original.Track(1, "100", "drone-test")
	original.Track(1, "101", "drone-test")
	original.Track(2, "102", "drone-test")
	// Simulate a server restart: the new service must recover persisted job IDs.
	restarted := &Service{Store: s, Enabled: true, Query: func(_ context.Context, ids []string) (map[string]JobState, error) {
		if !reflect.DeepEqual(ids, []string{"100", "101", "102"}) {
			t.Errorf("queried untracked jobs: %v", ids)
		}
		return map[string]JobState{"100": {Status: "RUN", Name: "drone-test"}, "101": {Status: "PEND", Name: "drone-test"}}, errors.New("job 102 unavailable")
	}}
	if err := restarted.Sample(ctx, now); err != nil {
		t.Fatal(err)
	}
	result, err := s.Read(ctx, []Repository{{ID: 1}, {ID: 2}}, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	last := len(result.Times) - 1
	a := result.Series[0].Values[last]
	val(t, a[0], 1)
	val(t, a[1], 2)
	val(t, a[2], 1)
	val(t, a[3], 2)
	b := result.Series[1].Values[last]
	val(t, b[0], 0)
	if b[2] != nil || b[3] != nil {
		t.Fatal("unknown job must be a gap")
	}
	if result.Series[0].Values[last-1][0] != nil {
		t.Fatal("missing frame was filled with zero")
	}
	restarted.Track(0, "100", "drone-test")
	restarted.Track(0, "101", "drone-test")
	restarted.Track(0, "102", "drone-test")
	restarted.Query = func(context.Context, []string) (map[string]JobState, error) {
		t.Fatal("queried LSF without tracked IDs")
		return nil, nil
	}
	if err := restarted.Sample(ctx, now.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	result, err = s.Read(ctx, []Repository{{ID: 1}}, now.Add(30*time.Second), 1)
	if err != nil {
		t.Fatal(err)
	}
	val(t, result.Series[0].Values[len(result.Times)-1][2], 0)
}
func TestRetentionPeakAndDeletionIndependence(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	repo := Repository{ID: 1}
	// Full 2-minute bucket for a 24h chart, with separate RUN and RUN+PEND peaks.
	start := now.Unix() / 120 * 120
	for i := int64(0); i < 4; i++ {
		run, pending := 1, 0
		if i == 1 {
			run = 3
		}
		if i == 2 {
			pending = 4
		}
		if err := s.Save(start+i*30, map[int64]*Counts{1: {LSFRunning: run, LSFPending: pending, LSFValid: true}}, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	result, err := s.Read(ctx, []Repository{repo}, time.Unix(start+90, 0), 24)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Series[0].Values[len(result.Times)-1]
	val(t, last[2], 3)
	val(t, last[3], 5)
	// Sampled history has no foreign keys to builds and survives record deletion.
	addBuild(t, s, 1, 1, "success")
	b, _ := build.New(s.DB).FindNumber(ctx, 1, 1)
	if err := build.New(s.DB).Delete(ctx, b); err != nil {
		t.Fatal(err)
	}
	result, err = s.Read(ctx, []Repository{repo}, time.Unix(start+90, 0), 24)
	if err != nil {
		t.Fatal(err)
	}
	val(t, result.Series[0].Values[len(result.Times)-1][2], 3)
	future := start + int64(Retention/time.Second) + 120
	if err := s.Save(future, map[int64]*Counts{}, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.View(func(q db.Queryer, _ db.Binder) error {
		var count int
		if err := q.QueryRow("SELECT COUNT(*) FROM trend_samples").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("expired samples retained: %d", count)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestDoneAndSuspendedJobs(t *testing.T) {
	s := fixture(t)
	service := &Service{Store: s, Enabled: true}
	states := map[string]JobState{"1": {Status: "DONE", Name: "drone-test"}, "2": {Status: "EXIT", Name: "drone-test"}, "3": {Status: "SSUSP", Name: "drone-test"}, "4": {Status: "WAIT", Name: "drone-test"}, "5": {Status: "PROV", Name: "drone-test"}, "6": {Status: "UNKWN", Name: "drone-test"}}
	for id := range states {
		service.Track(1, id, "drone-test")
	}
	service.Query = func(context.Context, []string) (map[string]JobState, error) { return states, nil }
	now := time.Unix(1800000000, 0)
	if err := service.Sample(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 4 {
		t.Fatalf("terminal jobs not retired: %v", jobs)
	}
	result, err := s.Read(context.Background(), []Repository{{ID: 1}}, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Series[0].Values[len(result.Times)-1][2] != nil {
		t.Fatal("unknown scheduler state hidden")
	}
}
func TestDisabledLSFAndTrackingFailure(t *testing.T) {
	s := fixture(t)
	service := &Service{Store: s, Enabled: false}
	now := time.Unix(1800000000, 0)
	if err := service.Sample(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := s.Read(context.Background(), []Repository{{ID: 1}}, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	last := result.Series[0].Values[len(result.Times)-1]
	val(t, last[0], 0)
	if last[2] != nil {
		t.Fatal("disabled LSF should be unknown")
	}
	// A failed tracking write must remain queued for retry, not silently disappear.
	if s.DB.Driver() == db.Sqlite {
		err = s.DB.Lock(func(tx db.Execer, _ db.Binder) error {
			_, e := tx.Exec("CREATE TRIGGER fail_tracking BEFORE INSERT ON trend_jobs BEGIN SELECT RAISE(ABORT,'test failure'); END")
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		service.Enabled = true
		service.Track(1, "123", "drone-test")
		if len(service.pending) != 1 {
			t.Fatal("failed write not retained")
		}
		if err = service.Sample(context.Background(), now); err != nil {
			t.Fatal(err)
		}
		result, err = s.Read(context.Background(), []Repository{{ID: 1}}, now, 1)
		if err != nil {
			t.Fatal(err)
		}
		if result.Series[0].Values[len(result.Times)-1][2] != nil {
			t.Fatal("tracking failure must leave a gap")
		}
		_ = s.DB.Lock(func(tx db.Execer, _ db.Binder) error { _, e := tx.Exec("DROP TRIGGER fail_tracking"); return e })
		if !service.flush() {
			t.Fatal("tracking recovery failed")
		}
		jobs, _ := s.Jobs()
		if jobs["123"].Repo != 1 {
			t.Fatal("job not recovered")
		}
	}
}
func TestTrackedQuery(t *testing.T) {
	s := &Service{Bjobs: "/bin/echo"}
	states, err := s.query(context.Background(), []string{"123"})
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 0 {
		t.Fatalf("accepted malformed output: %v", states)
	}
	if _, err = s.query(context.Background(), []string{"-a"}); err == nil {
		t.Fatal("accepted nonnumeric job id")
	}
	// Queries must never contain a no-ID request that could collect other users' jobs.
	if _, err = s.query(context.Background(), nil); err != nil {
		t.Fatal(fmt.Sprint(err))
	}
}

func TestReusedJobIDNeverCountsOtherJobs(t *testing.T) {
	s := fixture(t)
	service := &Service{Store: s, Enabled: true}
	service.Track(1, "100", "drone-unique-job")
	service.Query = func(context.Context, []string) (map[string]JobState, error) {
		return map[string]JobState{"100": {Status: "RUN", Name: "third-party-job"}}, nil
	}
	now := time.Unix(1800000000, 0)
	if err := service.Sample(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	result, err := s.Read(context.Background(), []Repository{{ID: 1}}, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Series[0].Values[len(result.Times)-1][2] != nil {
		t.Fatal("reused ID must not count unrelated work")
	}
	jobs, err := s.Jobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatal("reused ID should be retired")
	}
}

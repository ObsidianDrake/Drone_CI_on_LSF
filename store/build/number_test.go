package build

import (
	"context"
	"database/sql"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/repos"
	"github.com/drone/drone/store/shared/db/dbtest"
)

func TestCreateNextRollsBackNumberOnStageFailure(t *testing.T) {
	ctx := context.Background()
	d, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	repositories := repos.New(d)
	repo := &core.Repository{UID: "qc", Slug: "org/qc", Active: true, Counter: 5000}
	if err := repositories.Create(ctx, repo); err != nil {
		t.Fatal(err)
	}
	original := *repo
	store := New(d)
	sequence := store.(core.BuildSequenceStore)
	failed := &core.Build{RepoID: repo.ID, Status: core.StatusPending}
	// Duplicate stage numbers force insertion to fail after the counter update.
	stages := []*core.Stage{{Number: 1}, {Number: 1}}
	if err := sequence.CreateNext(ctx, repo, failed, stages); err == nil {
		t.Fatal("duplicate stage accepted")
	}
	saved, err := repositories.Find(ctx, repo.ID)
	if err != nil || saved.Counter != 5000 || saved.Version != original.Version || repo.Counter != 5000 || repo.Version != original.Version {
		t.Fatalf("counter escaped rollback: saved=%+v local=%+v error=%v", saved, repo, err)
	}
	if _, err := store.FindNumber(ctx, repo.ID, 5001); err != sql.ErrNoRows {
		t.Fatal("failed build record persisted")
	}
	next := &core.Build{RepoID: repo.ID, Status: core.StatusPending}
	if err := sequence.CreateNext(ctx, repo, next, []*core.Stage{{Number: 1}}); err != nil || next.Number != 5001 {
		t.Fatalf("next=%+v error=%v", next, err)
	}
}

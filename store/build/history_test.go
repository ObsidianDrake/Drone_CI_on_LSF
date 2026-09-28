package build

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/shared/db"
	"github.com/drone/drone/store/shared/db/dbtest"
)

func TestDeleteHistory(t *testing.T) {
	conn, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := New(conn).(*buildStore)
	seed := func(repo, number int64, status, stageStatus string) *core.Build {
		t.Helper()
		b := &core.Build{RepoID: repo, Number: number, Status: status, Event: core.EventPush, Ref: "refs/heads/main", Target: "main"}
		stages := []*core.Stage{{RepoID: repo, Number: 1, Status: stageStatus}}
		if err := store.Create(noContext, b, stages); err != nil {
			t.Fatal(err)
		}
		err := conn.Update(func(tx db.Execer, _ db.Binder) error {
			_, err := tx.Exec(fmt.Sprintf("INSERT INTO steps (step_id,step_stage_id,step_number,step_status) VALUES (%d,%d,1,'success')", b.ID, stages[0].ID))
			if err != nil {
				return err
			}
			_, err = tx.Exec(fmt.Sprintf("INSERT INTO logs (log_id,log_data) VALUES (%d,'log')", b.ID))
			if err != nil {
				return err
			}
			_, err = tx.Exec(fmt.Sprintf("INSERT INTO cards (card_id,card_data) VALUES (%d,'card')", b.ID))
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for i, status := range []string{"success", "failure", "error", "killed", "skipped", "pending", "running", "blocked", "waiting"} {
		seed(1, int64(i+1), status, status)
	}
	other := seed(2, 2, "failure", "failure")
	tearingDown := seed(1, 10, "killed", "running")
	preview, err := store.DeleteHistory(noContext, 1, core.HistoryFilter{Numbers: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, NonSuccess: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Numbers, []int64{2, 3, 4, 5}) {
		t.Fatalf("preview: %v", preview.Numbers)
	}
	if count, _ := store.Count(noContext); count != 11 {
		t.Fatalf("preview mutated database: %d", count)
	}
	_, err = store.DeleteHistory(noContext, 1, core.HistoryFilter{Before: 11}, preview.Token)
	if !errors.Is(err, core.ErrHistoryChanged) {
		t.Fatalf("different filter: %v", err)
	}
	filter := core.HistoryFilter{Numbers: []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, NonSuccess: true}
	result, err := store.DeleteHistory(noContext, 1, filter, preview.Token)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.StepIDs) != 4 {
		t.Fatalf("step IDs: %v", result.StepIDs)
	}
	for _, table := range []string{"builds", "stages", "steps", "logs", "cards"} {
		err = conn.View(func(q db.Queryer, _ db.Binder) error {
			var count int
			err := q.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
			if count != 7 {
				t.Errorf("%s retained %d rows, want 7", table, count)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, b := range []*core.Build{other, tearingDown} {
		if _, err := store.Find(noContext, b.ID); err != nil {
			t.Fatal("protected build removed", err)
		}
	}
	if _, err := store.DeleteHistory(noContext, 1, filter, preview.Token); !errors.Is(err, core.ErrHistoryChanged) {
		t.Fatalf("replay: %v", err)
	}
	// Strict before boundary preserves N; new completed records invalidate a preview.
	filter = core.HistoryFilter{Before: 2}
	preview, err = store.DeleteHistory(noContext, 1, filter, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Numbers, []int64{1}) {
		t.Fatalf("before: %v", preview.Numbers)
	}
	b, _ := store.FindNumber(noContext, 1, 1)
	b.Status = "error"
	if err := store.Update(noContext, b); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteHistory(noContext, 1, filter, preview.Token); !errors.Is(err, core.ErrHistoryChanged) {
		t.Fatalf("status change: %v", err)
	}
	seed(3, 1, "success", "success")
	seed(3, 2, "success", "success")
	filter = core.HistoryFilter{Numbers: []int64{2}}
	preview, err = store.DeleteHistory(noContext, 3, filter, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DeleteHistory(noContext, 3, filter, preview.Token); err != nil {
		t.Fatal(err)
	}
	branches, err := store.LatestBranches(noContext, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(branches) != 1 || branches[0].Number != 1 {
		t.Fatalf("latest index not restored: %+v", branches)
	}
}

func TestHistoryTransactionRollback(t *testing.T) {
	conn, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if conn.Driver() != db.Sqlite {
		t.Skip("SQLite failure injection")
	}
	store := New(conn).(*buildStore)
	for _, num := range []int64{1, 2} {
		if err := store.Create(noContext, &core.Build{RepoID: 1, Number: num, Status: "success"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	err = conn.Lock(func(tx db.Execer, _ db.Binder) error {
		_, err := tx.Exec("CREATE TRIGGER fail_history_delete BEFORE DELETE ON builds WHEN old.build_number = 2 BEGIN SELECT RAISE(ABORT, 'injected failure'); END")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	filter := core.HistoryFilter{Before: 3}
	preview, err := store.DeleteHistory(noContext, 1, filter, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteHistory(noContext, 1, filter, preview.Token); err == nil {
		t.Fatal("expected failure")
	}
	if count, _ := store.Count(noContext); count != 2 {
		t.Fatalf("partial deletion: %d", count)
	}
}

func TestHistoryNonSuccessAllPages(t *testing.T) {
	conn, err := dbtest.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	store := New(conn).(*buildStore)
	for number := int64(1); number <= 30; number++ {
		status := "success"
		if number == 2 {
			status = "failure"
		}
		if number == 28 {
			status = "error"
		}
		if number == 29 {
			status = "running"
		}
		if err := store.Create(noContext, &core.Build{RepoID: 1, Number: number, Status: status}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Create(noContext, &core.Build{RepoID: 2, Number: 1, Status: "failure"}, nil); err != nil {
		t.Fatal(err)
	}
	filter := core.HistoryFilter{NonSuccess: true}
	preview, err := store.DeleteHistory(noContext, 1, filter, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Numbers, []int64{2, 28}) {
		t.Fatalf("all-page preview: %v", preview.Numbers)
	}
	// A newly finished matching build invalidates the repository-wide confirmation.
	added := &core.Build{RepoID: 1, Number: 31, Status: "killed"}
	if err := store.Create(noContext, added, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteHistory(noContext, 1, filter, preview.Token); !errors.Is(err, core.ErrHistoryChanged) {
		t.Fatalf("stale preview: %v", err)
	}
	preview, err = store.DeleteHistory(noContext, 1, filter, "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteHistory(noContext, 1, filter, preview.Token)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Numbers, []int64{2, 28, 31}) {
		t.Fatalf("deleted: %v", result.Numbers)
	}
	for _, item := range []struct{ repo, number int64 }{{1, 1}, {1, 29}, {2, 1}} {
		if _, err := store.FindNumber(noContext, item.repo, item.number); err != nil {
			t.Fatalf("protected build removed: %+v: %v", item, err)
		}
	}
}

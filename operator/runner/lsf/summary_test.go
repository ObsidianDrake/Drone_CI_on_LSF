package lsf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone-runtime/engine"
)

func TestJobSummarySchedulerOutput(t *testing.T) {
	const report = "Sender: LSF System\r\nSubject: Job <42>: Done\r\n\r\nResource usage summary:\n    CPU time : 1.25 sec.\n\tMax Memory : 42 MB\n自訂欄位: 100% <value>\n"
	for _, test := range []struct {
		name, data, want   string
		missing, directory bool
	}{
		{name: "raw report", data: report, want: report},
		{name: "no final newline", data: "Exited with exit code 7.", want: "Exited with exit code 7.\n"},
		{name: "empty file", want: "scheduler.out is empty.\n"},
		{name: "missing file", missing: true, want: "scheduler.out is unavailable.\n"},
		{name: "unreadable file", directory: true, want: "scheduler.out is unavailable.\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "output.log")
			if err := os.WriteFile(path, []byte("original output\n"), 0600); err != nil {
				t.Fatal(err)
			}
			scheduler := filepath.Join(dir, "scheduler.out")
			if test.directory {
				if err := os.Mkdir(scheduler, 0700); err != nil {
					t.Fatal(err)
				}
			} else if !test.missing {
				if err := os.WriteFile(scheduler, []byte(test.data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			// The summary must never need a bjobs query, even with an invalid CLI.
			e := &Engine{config: Config{Bjobs: filepath.Join(dir, "nonexistent-bjobs"),
				LogQuietPeriod: 10 * time.Millisecond, LogDrainTimeout: 60 * time.Millisecond}}
			previousErr := errors.New("original step error")
			j := &job{id: "42", dir: dir, log: path, err: previousErr, state: engine.State{Exited: true, ExitCode: 7}}
			e.appendJobSummary(j)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := "original output\n\n\033[0;37m--- LSF job information (scheduler.out) ---\n" + test.want + "--- End LSF job information ---\n"
			if string(data) != want {
				t.Fatalf("got %q, want %q", data, want)
			}
			if j.err != previousErr || j.state.ExitCode != 7 || !j.state.Exited {
				t.Fatal("scheduler output changed the step result")
			}
		})
	}
}

func TestSchedulerOutputWaitsForLateReport(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "initially empty", true: "initially missing"}[missing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scheduler.out")
			if !missing {
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			writes := make(chan error, 1)
			go func() {
				// Initial absence lasts longer than the quiet period.
				time.Sleep(150 * time.Millisecond)
				if err := os.WriteFile(path, []byte("partial report\n"), 0600); err != nil {
					writes <- err
					return
				}
				time.Sleep(60 * time.Millisecond)
				// Same-size rewrite must also restart the quiet period.
				writes <- os.WriteFile(path, []byte("updated report\n"), 0600)
			}()
			data, err, stable := readSchedulerOutput(path, 100*time.Millisecond, time.Second)
			if writeErr := <-writes; writeErr != nil {
				t.Fatal(writeErr)
			}
			if err != nil || !stable || string(data) != "updated report\n" {
				t.Fatalf("data=%q err=%v stable=%v", data, err, stable)
			}
		})
	}
}

func TestSchedulerOutputDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler.out")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	data, err, stable := readSchedulerOutput(path, 10*time.Millisecond, 60*time.Millisecond)
	if err != nil || stable || len(data) != 0 || time.Since(start) < 60*time.Millisecond {
		t.Fatalf("empty report did not wait until deadline: data=%q err=%v stable=%v", data, err, stable)
	}
	if time.Since(start) > time.Second {
		t.Fatal("empty report exceeded bounded wait")
	}
}

func TestJobSummaryLatePartialReport(t *testing.T) {
	dir := t.TempDir()
	j := &job{id: "42", dir: dir, log: filepath.Join(dir, "output.log"),
		state: engine.State{Exited: true, ExitCode: 0}}
	if err := os.WriteFile(j.log, []byte("commands complete\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{config: Config{LogQuietPeriod: 100 * time.Millisecond, LogDrainTimeout: 150 * time.Millisecond}}
	written := make(chan error, 1)
	go func() {
		time.Sleep(80 * time.Millisecond)
		written <- os.WriteFile(filepath.Join(dir, "scheduler.out"), []byte("partial report\n"), 0600)
	}()
	e.appendJobSummary(j)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(j.log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "partial report\n[WARNING] scheduler.out did not stabilize") {
		t.Fatalf("lost partial report or missing deadline warning: %q", data)
	}
	if strings.Count(string(data), "--- LSF job information (scheduler.out) ---") != 1 {
		t.Fatal("duplicated scheduler report")
	}
	if j.err != nil || j.state.ExitCode != 0 {
		t.Fatal("report timeout changed successful job result")
	}
}

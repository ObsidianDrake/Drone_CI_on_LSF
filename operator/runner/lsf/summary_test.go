package lsf

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

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
			e := &Engine{config: Config{Bjobs: filepath.Join(dir, "nonexistent-bjobs")}}
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

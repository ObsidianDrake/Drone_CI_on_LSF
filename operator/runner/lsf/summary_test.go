package lsf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobDetailsWrappedAndMultipleHosts(t *testing.T) {
	input := `Job <42>, Job Name <test>, User <alice>, Status <DONE>, Queue <normal>, C
                     ommand </bin/sh /shared/run.sh>
Submitted from host <submit>, CWD </shared>, Output File </shared/scheduler.
                     out>, Error File </shared/scheduler.err>;
Started 2 Task(s) on Hosts <host1> <host2>, Execution Home </home/alice>, Execution CWD </shared/work>;
`
	got := jobDetails(input)
	for k, want := range map[string]string{"Job": "42", "Job Name": "test", "User": "alice", "Queue": "normal", "Command": "/bin/sh /shared/run.sh", "Host": "host1, host2", "CWD": "/shared/work", "Output File": "/shared/scheduler.out", "Error File": "/shared/scheduler.err"} {
		if got[k] != want {
			t.Errorf("%s: got %q, want %q", k, got[k], want)
		}
	}
}

func TestJobSummaryUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.log")
	if err := os.WriteFile(path, []byte("original output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e := &Engine{config: Config{Bjobs: "/bin/false", CommandTimeout: time.Second}}
	j := &job{id: "42", log: path}
	e.appendJobSummary(j)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"original output\n", "Job ID: 42", "Job details unavailable", "Host: N/A", "Error File: N/A"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %q: %s", want, data)
		}
	}
	if j.err != nil || j.state.ExitCode != 0 {
		t.Fatal("diagnostic query changed step result")
	}
}

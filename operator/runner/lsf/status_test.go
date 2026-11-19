package lsf

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBjobsStatusWithDiagnostics(t *testing.T) {
	const notice = "Cannot connect to LSF. Please wait ..."
	for _, tc := range []struct {
		name, out, diag     string
		fail, invalid, done bool
		code                int
	}{
		{name: "warning on stderr", out: "RUN -", diag: notice},
		{name: "inline retry", out: notice + " RUN -"},
		{name: "repeated retry", out: notice + "\r" + notice + "\nDONE 0", done: true},
		{name: "exit after retry", out: notice + " EXIT 137", done: true, code: 137},
		{name: "pending without exit column", out: "PEND"},
		{name: "warning only", out: notice, invalid: true},
		{name: "misleading diagnostic", out: "query failed: last status DONE 0", invalid: true},
		{name: "ignore status in stderr", diag: "DONE 0", invalid: true},
		{name: "ambiguous status", out: "RUN -\nDONE 0", invalid: true},
		{name: "bad exit code", out: "EXIT failed", invalid: true},
		{name: "nonzero command exit", out: "DONE 0", diag: notice, fail: true, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "bjobs")
			script := "#!/bin/sh\nprintf '%s\\n' " + quote(tc.out) + "\nprintf '%s\\n' " + quote(tc.diag) + " >&2\n"
			if tc.fail {
				script += "exit 1\n"
			}
			if err := os.WriteFile(file, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			e := &Engine{config: Config{Bjobs: file, CommandTimeout: time.Second}}
			state, done, err := e.status(context.Background(), "74108408")
			if (err != nil) != tc.invalid || done != tc.done || state.Exited != tc.done || state.ExitCode != tc.code {
				t.Fatalf("state=%+v done=%v err=%v", state, done, err)
			}
		})
	}
}

// More than three reconnect notices must not exhaust the monitor's error budget
// when every poll still reports the requested job's status.
func TestMonitorSurvivesBjobsReconnect(t *testing.T) {
	dir := t.TempDir()
	polls := filepath.Join(dir, "polls")
	killed := filepath.Join(dir, "killed")
	bjobs, bkill := filepath.Join(dir, "bjobs"), filepath.Join(dir, "bkill")
	if err := os.WriteFile(bjobs, []byte("#!/bin/sh\necho poll >> "+quote(polls)+"\nif [ \"$(wc -l < "+quote(polls)+")\" -le 4 ]; then echo 'Cannot connect to LSF. Please wait ... RUN -'; else echo 'DONE 0'; fi\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bkill, []byte("#!/bin/sh\ntouch "+quote(killed)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e := &Engine{config: Config{Bjobs: bjobs, Bkill: bkill, PollInterval: time.Millisecond, CommandTimeout: time.Second, CleanupTimeout: time.Second}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j := &job{id: "74108408", dir: dir, done: make(chan struct{})}
	e.monitor(&pipeline{ctx: ctx, jobInfoDisabled: true}, j)
	if !j.confirmed || !j.state.Exited || j.state.ExitCode != 0 || j.err != nil {
		t.Fatalf("state=%+v err=%v", j.state, j.err)
	}
	if _, err := os.Stat(killed); !os.IsNotExist(err) {
		t.Fatalf("unexpected bkill: %v", err)
	}
}

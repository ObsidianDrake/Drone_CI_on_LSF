package lsf

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone-runtime/engine"
)

func TestPendingCleanupSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "pipeline-test", "step-test")
	if err := os.MkdirAll(filepath.Join(dir, "clone-auth"), 0700); err != nil {
		t.Fatal(err)
	}
	j := &job{id: "123", dir: dir, wrapper: filepath.Join(dir, "run.sh")}
	if err := savePendingCleanup(j); err != nil {
		t.Fatal(err)
	}
	gate, killed := filepath.Join(root, "available"), filepath.Join(root, "killed")
	bjobs, bkill := filepath.Join(root, "bjobs"), filepath.Join(root, "bkill")
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(bjobs, "if [ \"$4\" = command ]; then printf '%s\\n' "+quote("/bin/sh "+j.wrapper)+"; elif [ -f "+quote(killed)+" ]; then echo 'EXIT 137'; else echo 'RUN -'; fi\n")
	write(bkill, "test -f "+quote(gate)+" || exit 1\ntouch "+quote(killed)+"\n")
	config := Config{Workspace: root, Bsub: "/bin/true", Bjobs: bjobs, Bkill: bkill, Shell: "/bin/sh", CommandTimeout: time.Second}
	e, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.cleanupPendingJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, pendingCleanupFile)); err != nil {
		t.Fatal("lost pending cleanup", err)
	}
	if err = os.WriteFile(gate, nil, 0600); err != nil {
		t.Fatal(err)
	}
	// A fresh engine has no in-memory pipeline/job records.
	restarted, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.cleanupPendingJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(killed); err != nil {
		t.Fatal("job not killed after recovery", err)
	}
	if _, err = os.Stat(filepath.Join(dir, pendingCleanupFile)); !os.IsNotExist(err) {
		t.Fatal("pending marker remains", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "clone-auth")); !os.IsNotExist(err) {
		t.Fatal("credentials remain", err)
	}
	if _, err = os.Stat(dir); err != nil {
		t.Fatal("diagnostics removed", err)
	}
}

func TestPendingCleanupRejectsReusedJobID(t *testing.T) {
	root := t.TempDir()
	killed := filepath.Join(root, "killed")
	query := filepath.Join(root, "bjobs")
	kill := filepath.Join(root, "bkill")
	os.WriteFile(query, []byte("#!/bin/sh\necho '/bin/sh /another/pipeline/run.sh'\n"), 0700)
	os.WriteFile(kill, []byte("#!/bin/sh\ntouch "+quote(killed)+"\n"), 0700)
	e := &Engine{config: Config{Bjobs: query, Bkill: kill, CommandTimeout: time.Second}}
	err := e.retryPendingJob(context.Background(), pendingCleanup{1, "123", "/original/pipeline/run.sh"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err = os.Stat(killed); !os.IsNotExist(err) {
		t.Fatal("killed unrelated job")
	}
}

func TestSlowStatusCannotConsumeFirstKillBudget(t *testing.T) {
	dir := t.TempDir()
	query, kill, killed := filepath.Join(dir, "bjobs"), filepath.Join(dir, "bkill"), filepath.Join(dir, "killed")
	os.WriteFile(query, []byte("#!/bin/sh\nsleep 0.1\necho 'RUN -'\n"), 0700)
	os.WriteFile(kill, []byte("#!/bin/sh\ntouch "+quote(killed)+"\n"), 0700)
	e := &Engine{config: Config{Bjobs: query, Bkill: kill, CommandTimeout: 200 * time.Millisecond, CleanupTimeout: 50 * time.Millisecond, PollInterval: time.Millisecond}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &pipeline{ctx: ctx, dir: dir, jobInfoDisabled: true}
	j := &job{id: "123", dir: dir, wrapper: filepath.Join(dir, "run.sh"), done: make(chan struct{})}
	e.monitor(p, j)
	if _, err := os.Stat(killed); err != nil {
		t.Fatal("slow pre-kill query prevented bkill", err)
	}
	if j.confirmed || j.state.Exited || j.state.ExitCode != 0 || j.err == nil {
		t.Fatal("termination incorrectly confirmed")
	}
	if _, err := os.Stat(filepath.Join(dir, pendingCleanupFile)); err != nil {
		t.Fatal("missing recovery record", err)
	}
}

func TestPendingCleanupSkipsLiveMonitor(t *testing.T) {
	j := &job{dir: "/step", done: make(chan struct{})}
	e := &Engine{pipelines: map[*engine.Spec]*pipeline{{}: {jobs: map[*engine.Step]*job{{}: j}}}}
	if !e.cleanupMonitorActive("/step") {
		t.Fatal("live monitor not excluded")
	}
	close(j.done)
	if e.cleanupMonitorActive("/step") {
		t.Fatal("finished monitor blocks recovery")
	}
}

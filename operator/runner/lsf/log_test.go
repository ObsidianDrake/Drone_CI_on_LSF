package lsf

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone-runtime/engine"
)

func logTestEngine(t *testing.T, initial string) (*Engine, *engine.Spec, *engine.Step, *job) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "output.log")
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	spec, step := &engine.Spec{}, &engine.Step{}
	j := &job{id: "123", dir: dir, log: path, done: make(chan struct{}), confirmed: true}
	p := &pipeline{ctx: ctx, cancel: cancel, dir: dir, jobs: map[*engine.Step]*job{step: j}}
	e := &Engine{config: Config{PollInterval: 5 * time.Millisecond, LogQuietPeriod: 150 * time.Millisecond, LogDrainTimeout: time.Second}, pipelines: map[*engine.Spec]*pipeline{spec: p}}
	return e, spec, step, j
}

func appendTestLog(path, data string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(data)
	return err
}

func TestTailCollectsLateOutputBeforeCleanup(t *testing.T) {
	for _, initial := range []string{"", "first\n"} {
		t.Run(initial, func(t *testing.T) {
			e, spec, step, j := logTestEngine(t, initial)
			step.Detach = true
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			r, err := e.Tail(ctx, spec, step)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			close(j.done)
			// Mimic execution-host writes becoming visible after terminal status.
			writes := make(chan error, 1)
			go func() {
				for _, part := range []string{"late-one\n", "late-two without newline"} {
					time.Sleep(100 * time.Millisecond)
					if err := appendTestLog(j.log, part); err != nil {
						writes <- err
						return
					}
				}
				writes <- nil
			}()
			cleanup := make(chan error, 1)
			go func() { cleanup <- e.Destroy(ctx, spec) }()
			data, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-writes; err != nil {
				t.Fatal(err)
			}
			if string(data) != initial+"late-one\nlate-two without newline" {
				t.Fatalf("incomplete or duplicated log: %q", data)
			}
			if err := <-cleanup; err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(j.log); !os.IsNotExist(err) {
				t.Fatalf("normal cleanup did not remove log: %v", err)
			}
		})
	}
}

func TestTailDrainDeadlineRetainsFiles(t *testing.T) {
	e, spec, step, j := logTestEngine(t, "first\n")
	e.config.LogQuietPeriod = 100 * time.Millisecond
	e.config.LogDrainTimeout = 250 * time.Millisecond
	r, err := e.Tail(context.Background(), spec, step)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	close(j.done)
	stopWrites := make(chan struct{})
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrites:
				return
			case <-ticker.C:
				if err := appendTestLog(j.log, "still writing\n"); err != nil {
					return
				}
			}
		}
	}()
	data, err := io.ReadAll(r)
	close(stopWrites)
	<-writeDone
	if err == nil || !strings.Contains(err.Error(), "did not settle") || !strings.Contains(string(data), "still writing\n") {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if err := e.Destroy(context.Background(), spec); err == nil {
		t.Fatal("cleanup ignored log collection failure")
	}
	if _, err := os.Stat(j.log); err != nil {
		t.Fatalf("lost diagnostic log: %v", err)
	}
}

func TestTailCancellationAndReaderClose(t *testing.T) {
	for _, closeReader := range []bool{false, true} {
		e, spec, step, j := logTestEngine(t, "")
		ctx, cancel := context.WithCancel(context.Background())
		r, err := e.Tail(ctx, spec, step)
		if err != nil {
			t.Fatal(err)
		}
		if closeReader {
			r.Close()
		} else {
			cancel()
			if _, err := io.ReadAll(r); !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		}
		select {
		case <-j.logDone:
		case <-time.After(time.Second):
			t.Fatal("log reader leaked after cancellation/close")
		}
		r.Close()
		cancel()
	}
}

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
	"github.com/drone/drone-yaml/yaml"
	"github.com/drone/drone/internal/pipelineruntime"
)

type lifecycleBackend struct {
	engine.Engine
	destroyed chan struct{}
	err       error
}

func (e *lifecycleBackend) Destroy(context.Context, *engine.Spec) error {
	close(e.destroyed)
	return e.err
}

func (e *lifecycleBackend) Tail(context.Context, *engine.Spec, *engine.Step) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("service output\n")), nil
}

func TestLifecycleWaitsForDetachedLogUpload(t *testing.T) {
	backend := &lifecycleBackend{destroyed: make(chan struct{}), err: errors.New("termination unconfirmed")}
	e := WithContext(backend, context.Background())
	spec := &engine.Spec{}
	r, err := e.Tail(context.Background(), spec, &engine.Step{Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := io.ReadAll(r); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.Destroy(context.Background(), spec) }()
	<-backend.destroyed
	select {
	case <-done:
		t.Fatal("Destroy returned before the detached log upload finished")
	default:
	}
	// runtime.stream calls Close only after GotLogs returns.
	r.Close()
	select {
	case err := <-done:
		if err != backend.err || e.CleanupErr != backend.err {
			t.Fatalf("lost cleanup error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Destroy did not finish after stream close")
	}
}

func TestDetachedCompileAndCloneValidation(t *testing.T) {
	spec := testSpec(t, `kind: pipeline
type: lsf
clone: {disable: true}
steps:
- name: service
  detach: true
  commands: [sleep 60]
- name: consumer
  depends_on: [service]
  commands: [echo consume]
`)
	if len(spec.Steps) != 2 || !spec.Steps[0].Detach || spec.Steps[1].Detach {
		t.Fatalf("detach flag lost: %+v", spec.Steps)
	}
	if len(spec.Steps[1].DependsOn) != 1 || spec.Steps[1].DependsOn[0] != "service" {
		t.Fatal("detached dependency lost")
	}
}

func TestDetachedCloneRejected(t *testing.T) {
	m, err := yaml.ParseString("kind: pipeline\ntype: lsf\nclone: {disable: true}\nsteps:\n- name: clone\n  image: git\n  detach: true\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := Lint(m.Resources[0].(*yaml.Pipeline), true); err == nil {
		t.Fatal("detached clone accepted")
	}
}

func TestDetachedPendingCleanup(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		t.Run(map[bool]string{true: "confirmed", false: "unconfirmed"}[confirmed], func(t *testing.T) {
			dir := t.TempDir()
			command := func(name, script string) string {
				path := filepath.Join(dir, name)
				if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0700); err != nil {
					t.Fatal(err)
				}
				return path
			}
			flag := filepath.Join(dir, "killed")
			bsub := command("bsub", "echo 'Job <123> is submitted'\n")
			bkill := command("bkill", "touch "+quote(flag)+"\n")
			status := "echo PEND\n"
			if confirmed {
				status = "if [ -f " + quote(flag) + " ]; then echo 'EXIT 137'; else echo PEND; fi\n"
			}
			bjobs := command("bjobs", status)
			workspace := filepath.Join(dir, "work")
			e, err := New(Config{Bsub: bsub, Bjobs: bjobs, Bkill: bkill, Shell: "/bin/sh", Workspace: workspace, PollInterval: 5 * time.Millisecond, CommandTimeout: time.Second, CleanupTimeout: 100 * time.Millisecond})
			if err != nil {
				t.Fatal(err)
			}
			spec := testSpec(t, `kind: pipeline
type: lsf
clone: {disable: true}
steps:
- name: service
  detach: true
  commands: [sleep 60]
`)
			spec.Metadata.Labels = map[string]string{DebugRetainLabel: "true"}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			life := WithContext(e, ctx)
			r := runtime.New(runtime.WithEngine(life), runtime.WithConfig(spec))
			if err := r.Run(ctx); err != nil {
				t.Fatal(err)
			}
			if (life.CleanupErr == nil) != confirmed {
				t.Fatalf("cleanup error: %v", life.CleanupErr)
			}
			result := life.DetachedResults[spec.Steps[0]]
			if result.Confirmed != confirmed {
				t.Fatalf("result: %+v", result)
			}
			if _, err := os.Stat(flag); err != nil {
				t.Fatal("pending service was not killed", err)
			}
			dirs, _ := filepath.Glob(filepath.Join(workspace, "pipeline-*"))
			if len(dirs) != 1 {
				t.Fatalf("retained dirs: %v", dirs)
			}
			_, err = os.Stat(filepath.Join(dirs[0], debugRetentionFile))
			if confirmed && err != nil {
				t.Fatal(err)
			}
			if !confirmed && !os.IsNotExist(err) {
				t.Fatal("unconfirmed service eligible for expiry")
			}

		})
	}
}

package lsf

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drone/drone-runtime/engine"
)

// These lifecycle/cleanup tests do not need an LSF installation or tcsh.
func retentionEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Config{Bsub: "/bin/true", Bjobs: "/bin/true", Bkill: "/bin/true", Shell: "/bin/sh", Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func retentionPipeline(t *testing.T, e *Engine, debug bool) (*engine.Spec, *pipeline) {
	t.Helper()
	spec := &engine.Spec{Metadata: engine.Metadata{Labels: map[string]string{
		DebugRetainLabel: "false", "lsf.drone.io/repo-id": "12", "lsf.drone.io/build-id": "34", "lsf.drone.io/stage-id": "56",
	}}}
	if debug {
		spec.Metadata.Labels[DebugRetainLabel] = "true"
	}
	if err := e.Setup(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	p, _ := e.lookup(spec)
	t.Cleanup(p.cancel)
	return spec, p
}

func TestDebugRetentionCompletionAndRestart(t *testing.T) {
	e := retentionEngine(t)
	spec, p := retentionPipeline(t, e, true)
	if e.config.DebugRetention != 168*time.Hour || e.config.DebugCleanupInterval != time.Hour {
		t.Fatal("incorrect defaults")
	}
	before := time.Now().UTC()
	if err := e.Destroy(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(e.config.Workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	m, err := readDebugRetention(root, filepath.Base(p.dir))
	if err != nil {
		t.Fatal(err)
	}
	if m.CompletedAt.Before(before) || m.CompletedAt.After(time.Now()) || m.ExpiresAt.Sub(m.CompletedAt) != 168*time.Hour || m.RepoID != 12 || m.BuildID != 34 || m.StageID != 56 {
		t.Fatalf("completion metadata: %+v", m)
	}
	info, _ := os.Stat(filepath.Join(p.dir, debugRetentionFile))
	if info.Mode().Perm() != 0600 {
		t.Fatalf("metadata permissions: %v", info.Mode())
	}
	// A fresh engine has no previous in-memory state; the persisted expiry wins
	// even if the configured retention period changes after restart.
	restarted := retentionEngine(t)
	restarted.config.Workspace = e.config.Workspace
	restarted.config.DebugRetention = time.Second
	if err := restarted.cleanupDebug(context.Background(), m.ExpiresAt.Add(-time.Nanosecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.dir); err != nil {
		t.Fatal("removed before expiry", err)
	}
	if err := restarted.cleanupDebug(context.Background(), m.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.dir); !os.IsNotExist(err) {
		t.Fatal("expired directory remains", err)
	}
}

func TestDebugRetentionEligibility(t *testing.T) {
	for _, mode := range []string{"normal", "confirmed-success", "confirmed-failure", "confirmed-cancel", "unconfirmed", "submission-unknown", "destroy-timeout"} {
		t.Run(mode, func(t *testing.T) {
			e := retentionEngine(t)
			spec, p := retentionPipeline(t, e, mode != "normal")
			ctx := context.Background()
			j := &job{id: "123", done: make(chan struct{})}
			p.jobs[&engine.Step{}] = j
			j.confirmed = mode != "unconfirmed" && mode != "destroy-timeout"
			if mode == "confirmed-failure" {
				j.state.ExitCode = 1
			}
			if mode == "confirmed-cancel" {
				j.state.ExitCode = 137
			}
			if mode == "submission-unknown" {
				p.submissionErr = errors.New("unknown submission")
			}
			if mode == "destroy-timeout" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			} else {
				close(j.done)
			}
			err := e.Destroy(ctx, spec)
			uncertain := mode == "unconfirmed" || mode == "submission-unknown" || mode == "destroy-timeout"
			if (err != nil) != uncertain {
				t.Fatalf("Destroy: %v", err)
			}
			if mode == "normal" {
				if _, err := os.Stat(p.dir); !os.IsNotExist(err) {
					t.Fatal("normal workdir retained")
				}
				return
			}
			_, err = os.Stat(filepath.Join(p.dir, debugRetentionFile))
			if uncertain != os.IsNotExist(err) {
				t.Fatalf("unexpected metadata state: %v", err)
			}
			if err := e.cleanupDebug(context.Background(), time.Now().Add(365*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(p.dir)
			if uncertain == os.IsNotExist(err) {
				t.Fatalf("unexpected retention state: %v", err)
			}
		})
	}
}

func TestDebugCleanupSafety(t *testing.T) {
	for _, mode := range []string{"active", "legacy", "corrupt", "version", "reason", "directory", "zero-time", "invalid-expiry", "oversized", "metadata-symlink", "pipeline-symlink", "unrelated", "expired-with-symlink"} {
		t.Run(mode, func(t *testing.T) {
			e := retentionEngine(t)
			spec, p := retentionPipeline(t, e, true)
			if mode != "active" {
				delete(e.pipelines, spec)
			}
			now := time.Now().UTC()
			m := debugRetention{Version: 1, Reason: "debug", Directory: filepath.Base(p.dir), CompletedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
			switch mode {
			case "version":
				m.Version = 2
			case "reason":
				m.Reason = "unknown"
			case "directory":
				m.Directory = "../other"
			case "zero-time":
				m.CompletedAt = time.Time{}
			case "invalid-expiry":
				m.ExpiresAt = m.CompletedAt
			}
			if err := writeDebugRetention(p.dir, m); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(p.dir, debugRetentionFile)
			outside := t.TempDir()
			keep := filepath.Join(outside, "keep")
			if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "legacy":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(marker, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(marker, make([]byte, 4097), 0600); err != nil {
					t.Fatal(err)
				}
			case "metadata-symlink":
				data, _ := json.Marshal(m)
				if err := os.WriteFile(keep, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(keep, marker); err != nil {
					t.Fatal(err)
				}
			case "pipeline-symlink":
				destination := filepath.Join(outside, filepath.Base(p.dir))
				if err := os.Rename(p.dir, destination); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(destination, p.dir); err != nil {
					t.Fatal(err)
				}
			case "unrelated":
				destination := filepath.Join(e.config.Workspace, "other")
				if err := os.Rename(p.dir, destination); err != nil {
					t.Fatal(err)
				}
				p.dir = destination
				m.Directory = "other"
				if err := writeDebugRetention(p.dir, m); err != nil {
					t.Fatal(err)
				}
			case "expired-with-symlink":
				if err := os.Symlink(outside, filepath.Join(p.dir, "linked")); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.cleanupDebug(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			_, err := os.Lstat(p.dir)
			if mode == "expired-with-symlink" {
				if !os.IsNotExist(err) {
					t.Fatal("expired dir remains", err)
				}
			} else if err != nil {
				t.Fatal("unsafe deletion", err)
			}
			if _, err := os.Stat(keep); err != nil {
				t.Fatal("symlink target removed", err)
			}
		})
	}
}

func TestDebugCleanupLoop(t *testing.T) {
	e := retentionEngine(t)
	e.config.DebugCleanupInterval = 10 * time.Millisecond
	_, p := retentionPipeline(t, e, true)
	e.pipelines = make(map[*engine.Spec]*pipeline)
	expired := time.Now().Add(-time.Hour)
	if err := e.retainDebug(p, expired.Add(-e.config.DebugRetention)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { e.RunDebugCleanup(ctx); close(done) }()
	waitRemoved := func(dir string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				return
			}
			select {
			case <-deadline:
				t.Fatal("cleanup did not run")
			case <-time.After(time.Millisecond):
			}
		}
	}
	waitRemoved(p.dir) // Startup scan.
	// A later completed directory requires a periodic scan.
	spec, p2 := retentionPipeline(t, e, true)
	if err := e.retainDebug(p2, expired.Add(-e.config.DebugRetention)); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	delete(e.pipelines, spec)
	e.mu.Unlock()
	waitRemoved(p2.dir)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not stop")
	}
}

func TestDebugRetentionConfigValidation(t *testing.T) {
	for _, c := range []Config{{DebugRetention: -time.Second}, {DebugCleanupInterval: -time.Second}} {
		if _, err := New(c); err == nil {
			t.Fatal("negative duration accepted")
		}
	}
}

func TestDebugRetentionWaitsForJobs(t *testing.T) {
	e := retentionEngine(t)
	spec, p := retentionPipeline(t, e, true)
	j := &job{id: "123", done: make(chan struct{})}
	p.jobs[&engine.Step{}] = j
	result := make(chan error, 1)
	go func() { result <- e.Destroy(context.Background(), spec) }()
	<-p.ctx.Done() // Destroy has started, but the job has not confirmed termination.
	marker := filepath.Join(p.dir, debugRetentionFile)
	_, markerErr := os.Stat(marker)
	cleanupErr := e.cleanupDebug(context.Background(), time.Now().Add(365*24*time.Hour))
	j.confirmed = true // Published to Destroy by closing done.
	close(j.done)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if !os.IsNotExist(markerErr) || cleanupErr != nil {
		t.Fatalf("workdir became eligible before job completion: %v, %v", markerErr, cleanupErr)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("missing completion metadata", err)
	}
}

func TestDebugRetentionWriteFailure(t *testing.T) {
	e := retentionEngine(t)
	spec, p := retentionPipeline(t, e, true)
	// A directory at the marker path makes the atomic rename fail.
	if err := os.Mkdir(filepath.Join(p.dir, debugRetentionFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.Destroy(context.Background(), spec); err == nil {
		t.Fatal("metadata write failure ignored")
	}
	if err := e.cleanupDebug(context.Background(), time.Now().Add(365*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.dir); err != nil {
		t.Fatal("workdir removed after metadata write failure", err)
	}
}

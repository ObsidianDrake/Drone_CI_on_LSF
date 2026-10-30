package lsf

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const pendingCleanupFile = ".drone-lsf-cleanup.json"

var cleanupIDPattern = regexp.MustCompile(`^[1-9][0-9]*$`)

type pendingCleanup struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Wrapper string `json:"wrapper"`
}

// Persist before attempting cancellation, so a server restart can resume it.
func savePendingCleanup(j *job) error {
	f, err := os.CreateTemp(j.dir, ".cleanup-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(pendingCleanup{1, j.id, j.wrapper})
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(j.dir, pendingCleanupFile))
}

// RunJobCleanup is owned by the server, independent of build cancellation.
func (e *Engine) RunJobCleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := e.cleanupPendingJobs(ctx); err != nil && ctx.Err() == nil {
			logrus.WithError(err).Warn("lsf: pending job cleanup scan failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Engine) cleanupPendingJobs(ctx context.Context) error {
	root, err := os.OpenRoot(e.config.Workspace)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	dirs, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := dirs.ReadDir(-1)
	dirs.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "pipeline-") {
			continue
		}
		dir, err := root.OpenRoot(entry.Name())
		if err != nil {
			continue
		}
		f, err := dir.Open(".")
		if err != nil {
			dir.Close()
			continue
		}
		steps, err := f.ReadDir(-1)
		f.Close()
		if err != nil {
			dir.Close()
			continue
		}
		for _, step := range steps {
			if ctx.Err() != nil {
				dir.Close()
				return ctx.Err()
			}
			if !step.IsDir() || !strings.HasPrefix(step.Name(), "step-") {
				continue
			}
			stepPath := filepath.Join(e.config.Workspace, entry.Name(), step.Name())
			if e.cleanupMonitorActive(stepPath) {
				continue
			}
			rel := filepath.Join(step.Name(), pendingCleanupFile)
			info, err := dir.Lstat(rel)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Size() > 8192 {
				continue
			}
			data, err := dir.ReadFile(rel)
			if err != nil {
				continue
			}
			var record pendingCleanup
			if json.Unmarshal(data, &record) != nil || record.Version != 1 || !cleanupIDPattern.MatchString(record.ID) || record.Wrapper != filepath.Join(stepPath, "run.sh") {
				logrus.WithField("directory", stepPath).Warn("lsf: invalid pending cleanup record; manual review required")
				continue
			}
			if err := e.retryPendingJob(ctx, record); err != nil {
				logrus.WithError(err).WithField("job", record.ID).Warn("lsf: termination unconfirmed; background cleanup will retry")
				continue
			}
			// Keep diagnostic output: the original log upload may already have failed.
			if err := dir.RemoveAll(filepath.Join(step.Name(), "clone-auth")); err != nil {
				continue
			}
			if err := dir.Remove(rel); err != nil {
				continue
			}
			if e.TrackJob != nil {
				e.TrackJob(0, record.ID, "")
			}
			logrus.WithField("job", record.ID).WithField("directory", stepPath).Info("lsf: background cleanup confirmed termination; diagnostic files retained")
		}
		dir.Close()
	}
	return nil
}

func (e *Engine) cleanupMonitorActive(path string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, p := range e.pipelines {
		p.mu.Lock()
		for _, j := range p.jobs {
			if j.dir == path {
				select {
				case <-j.done:
					p.mu.Unlock()
					return false
				default:
					p.mu.Unlock()
					return true
				}
			}
		}
		p.mu.Unlock()
	}
	return false
}

func (e *Engine) retryPendingJob(ctx context.Context, record pendingCleanup) error {
	// Verify the unique wrapper path before reusing an ID after a restart.
	// Never kill an unrelated job when LSF has recycled a numeric job ID.
	output, err := e.command(ctx, e.config.Bjobs, "-a", "-noheader", "-o", "command", record.ID)
	if err != nil {
		return err
	}
	if command := strings.TrimSpace(string(output)); command != "/bin/sh "+record.Wrapper && command != "/bin/sh "+quote(record.Wrapper) {
		return fmt.Errorf("lsf: job %s command does not match saved wrapper; manual review required", record.ID)
	}
	if _, done, err := e.status(ctx, record.ID); err == nil && done {
		return nil
	}
	// Each command gets its own timeout; slow status queries cannot exhaust a
	// shared cleanup budget and prevent the subsequent kill from being started.
	_, killErr := e.command(ctx, e.config.Bkill, record.ID)
	if _, done, err := e.status(ctx, record.ID); err == nil && done {
		return nil
	}
	return fmt.Errorf("lsf: job %s termination unconfirmed (bkill: %v)", record.ID, killErr)
}

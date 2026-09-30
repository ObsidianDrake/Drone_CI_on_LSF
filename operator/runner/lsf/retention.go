package lsf

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const debugRetentionFile = ".drone-lsf-retention.json"

// Only Destroy, after confirming all submitted jobs ended, publishes this file.
// Expiry is persisted so restart or a config change cannot reset retention.
type debugRetention struct {
	Version     int       `json:"version"`
	Reason      string    `json:"reason"`
	Directory   string    `json:"directory"`
	RepoID      int64     `json:"repo_id"`
	BuildID     int64     `json:"build_id"`
	StageID     int64     `json:"stage_id"`
	CompletedAt time.Time `json:"completed_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func (e *Engine) retainDebug(p *pipeline, completed time.Time) error {
	m := debugRetention{
		Version: 1, Reason: "debug", Directory: filepath.Base(p.dir),
		RepoID: p.repoID, BuildID: p.buildID, StageID: p.stageID,
		CompletedAt: completed, ExpiresAt: completed.Add(e.config.DebugRetention),
	}
	err := writeDebugRetention(p.dir, m)
	logger := logrus.WithFields(logrus.Fields{
		"directory": p.dir, "build_id": p.buildID, "stage_id": p.stageID,
		"completed_at": m.CompletedAt, "expires_at": m.ExpiresAt,
	})
	if err != nil {
		logger.WithError(err).Error("lsf: cannot save Debug expiry; directory retained for manual review")
		return err
	}
	logger.Info("lsf: Debug pipeline directory retained until expiry")
	return nil
}

func writeDebugRetention(dir string, m debugRetention) error {
	f, err := os.CreateTemp(dir, ".retention-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(m)
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, debugRetentionFile))
}

// RunDebugCleanup belongs to the server runner lifecycle, not individual builds.
// Failed scans/deletions are logged and retried at the next interval.
func (e *Engine) RunDebugCleanup(ctx context.Context) {
	ticker := time.NewTicker(e.config.DebugCleanupInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := e.cleanupDebug(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
			logrus.WithError(err).Warn("lsf: Debug cleanup scan failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (e *Engine) cleanupDebug(ctx context.Context, now time.Time) error {
	root, err := os.OpenRoot(e.config.Workspace)
	if os.IsNotExist(err) {
		return nil // No builds have run yet.
	}
	if err != nil {
		return err
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			name := entry.Name()
			if !entry.IsDir() || !strings.HasPrefix(name, "pipeline-") {
				continue // Includes symlinks: never traverse a linked pipeline.
			}
			dir := filepath.Join(e.config.Workspace, name)
			e.mu.Lock()
			active := false
			for _, p := range e.pipelines {
				if p.dir == dir {
					active = true
					break
				}
			}
			e.mu.Unlock()
			if active {
				continue
			}
			logger := logrus.WithField("directory", dir)
			m, err := readDebugRetention(root, name)
			if err != nil {
				logger.WithError(err).Warn("lsf: directory without valid Debug completion metadata retained for manual review")
				continue
			}
			if now.Before(m.ExpiresAt) {
				continue
			}
			logger = logger.WithFields(logrus.Fields{"build_id": m.BuildID, "stage_id": m.StageID, "expires_at": m.ExpiresAt})
			// Root confines deletion to the configured workspace. RemoveAll does
			// not follow symlinks inside the expired directory.
			if err := removeExpiredDebug(root, name, m); err != nil {
				logger.WithError(err).Warn("lsf: expired Debug directory cleanup failed")
				continue
			}
			logger.Info("lsf: expired Debug directory removed")
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func readDebugRetention(root *os.Root, name string) (debugRetention, error) {
	var m debugRetention
	path := filepath.Join(name, debugRetentionFile)
	info, err := root.Lstat(path)
	if err != nil {
		return m, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4096 {
		return m, fmt.Errorf("invalid retention metadata file")
	}
	f, err := root.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return m, err
	}
	if len(data) > 4096 {
		return m, fmt.Errorf("retention metadata too large")
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Version != 1 || m.Reason != "debug" || m.Directory != name || m.CompletedAt.IsZero() || !m.ExpiresAt.After(m.CompletedAt) {
		return m, fmt.Errorf("invalid Debug completion metadata")
	}
	return m, nil
}

// Keep the completion marker until payload deletion succeeds so an interrupted
// or failed cleanup can be retried, including after a server restart.
func removeExpiredDebug(root *os.Root, name string, m debugRetention) error {
	dir, err := root.OpenRoot(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		entries, readErr := f.ReadDir(128)
		for _, entry := range entries {
			if entry.Name() == debugRetentionFile {
				continue
			}
			if err := dir.RemoveAll(entry.Name()); err != nil {
				return err
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if err := dir.Remove(debugRetentionFile); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		data, _ := json.Marshal(m)
		if restoreErr := dir.WriteFile(debugRetentionFile, data, 0600); restoreErr != nil {
			return fmt.Errorf("%w; restore completion metadata: %v", err, restoreErr)
		}
		return err
	}
	return nil
}

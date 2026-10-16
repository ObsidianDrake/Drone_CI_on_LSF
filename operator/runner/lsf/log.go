package lsf

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

type logReader struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (r *logReader) Close() error {
	r.cancel()
	return r.PipeReader.Close()
}

// Scheduler completion is not a visibility barrier for shared filesystems.
// Reopen at EOF to refresh close-to-open caches, retaining the byte offset.
// Require a quiet interval after completion and after every late write.
func drainLog(ctx context.Context, file *os.File, out io.Writer, done <-chan struct{}, poll, quiet, timeout time.Duration) error {
	defer func() { file.Close() }()
	path := file.Name()
	buf := make([]byte, 32*1024)
	var offset int64
	var completed, changed time.Time
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if completed.IsZero() {
			select {
			case <-done:
				completed = time.Now()
				changed = completed
			default:
			}
		}
		if !completed.IsZero() && time.Since(completed) >= timeout {
			return fmt.Errorf("lsf: log did not settle within %s (read %d bytes)", timeout, offset)
		}
		n, err := file.Read(buf)
		if n > 0 {
			offset += int64(n)
			changed = time.Now()
			// Preserve the legacy runtime's one-line-per-write contract.
			for _, part := range bytes.SplitAfter(buf[:n], []byte{'\n'}) {
				if len(part) > 0 {
					if _, writeErr := out.Write(part); writeErr != nil {
						return writeErr
					}
				}
			}
		}
		if err != nil && err != io.EOF {
			return err
		}
		if n > 0 {
			continue
		}
		if !completed.IsZero() {
			fresh, err := os.Open(path)
			if err != nil {
				return fmt.Errorf("lsf: refreshing step log: %w", err)
			}
			info, err := fresh.Stat()
			if err == nil && info.Size() < offset {
				err = fmt.Errorf("lsf: step log was truncated from %d to %d bytes", offset, info.Size())
			}
			if err == nil {
				_, err = fresh.Seek(offset, io.SeekStart)
			}
			if err != nil {
				fresh.Close()
				return err
			}
			file.Close()
			file = fresh
			if info.Size() > offset {
				continue
			}
			if time.Since(changed) >= quiet {
				return nil
			}
		}
		interval := poll
		waitDone := done
		if !completed.IsZero() {
			waitDone = nil // a closed channel must not bypass the polling delay
			interval = min(poll, 250*time.Millisecond, time.Until(changed.Add(quiet)), time.Until(completed.Add(timeout)))
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-waitDone:
			timer.Stop()
		case <-timer.C:
		}
	}
}

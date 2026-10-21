package lsf

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// JobInfoDisabledLabel is set by the server from repository settings.
const JobInfoDisabledLabel = "lsf.drone.io/job-info-disabled"

func (e *Engine) appendJobSummary(j *job) {
	data, readErr, stable := readSchedulerOutput(filepath.Join(j.dir, "scheduler.out"),
		e.config.LogQuietPeriod, e.config.LogDrainTimeout)
	if !stable && readErr == nil {
		logrus.WithField("job", j.id).WithField("bytes", len(data)).
			Warn("lsf: scheduler.out did not become nonempty and stable before the read deadline")
	}
	var text strings.Builder
	text.WriteString("\n\033[0;37m--- LSF job information (scheduler.out) ---\n")
	switch {
	case readErr != nil:
		text.WriteString("scheduler.out is unavailable.\n")
		logrus.WithError(readErr).WithField("job", j.id).Warn("lsf: cannot read scheduler.out")
	case len(data) == 0:
		text.WriteString("scheduler.out is empty.\n")
	default:
		// Preserve the scheduler's report verbatim, including whitespace and
		// resource usage fields, rather than reconstructing it from bjobs.
		text.Write(data)
		if data[len(data)-1] != '\n' {
			text.WriteByte('\n')
		}
		if !stable {
			text.WriteString("[WARNING] scheduler.out did not stabilize before the read deadline; the report may be incomplete.\n")
		}
	}
	text.WriteString("--- End LSF job information ---\n")
	file, err := os.OpenFile(j.log, os.O_WRONLY|os.O_APPEND, 0600)
	if err == nil {
		_, err = file.WriteString(text.String())
		file.Close()
	}
	if err != nil {
		logrus.WithError(err).WithField("job", j.id).Warn("lsf: cannot append job information")
	}
}

// Terminal job status can precede publication of the scheduler report. Unlike
// command output, an empty scheduler report is not considered settled. Reopen
// on every read to refresh shared-filesystem caches, and compare contents so
// even same-size rewrites restart the quiet period. Cancellation also collects
// the report: this bounded wait deliberately does not use the canceled job ctx.
func readSchedulerOutput(path string, quiet, timeout time.Duration) ([]byte, error, bool) {
	if quiet <= 0 {
		quiet = 2 * time.Second
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	deadline := time.Now().Add(timeout)
	var previous []byte
	var unchangedSince time.Time
	for {
		data, err := os.ReadFile(path)
		now := time.Now()
		if err != nil && !os.IsNotExist(err) {
			return data, err, false
		}
		if err != nil || len(data) == 0 {
			unchangedSince = time.Time{}
		} else if unchangedSince.IsZero() || !bytes.Equal(data, previous) {
			unchangedSince = now
		}
		previous = data
		if !unchangedSince.IsZero() && now.Sub(unchangedSince) >= quiet {
			return data, nil, true
		}
		if !now.Before(deadline) {
			return data, err, false
		}
		interval := min(250*time.Millisecond, time.Until(deadline))
		if !unchangedSince.IsZero() {
			interval = min(interval, time.Until(unchangedSince.Add(quiet)))
		} else {
			interval = min(interval, quiet)
		}
		time.Sleep(max(interval, 0))
	}
}

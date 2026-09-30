package lsf

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// JobInfoDisabledLabel is set by the server from repository settings.
const JobInfoDisabledLabel = "lsf.drone.io/job-info-disabled"

func (e *Engine) appendJobSummary(j *job) {
	data, readErr := os.ReadFile(filepath.Join(j.dir, "scheduler.out"))
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

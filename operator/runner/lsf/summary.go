package lsf

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"
)

// JobInfoDisabledLabel is set by the server from repository settings.
const JobInfoDisabledLabel = "lsf.drone.io/job-info-disabled"

// LSF wraps long records with indentation, including in the middle of words.
var detailContinuation = regexp.MustCompile(`\r?\n[ \t]+`)
var executionHosts = regexp.MustCompile(`Started[^\n<]*((?:<[^>]+>[ \t]*)+)`)
var angleValue = regexp.MustCompile(`<([^>]+)>`)

func jobDetails(data string) map[string]string {
	data = detailContinuation.ReplaceAllString(data, "")
	fields := make(map[string]string)
	for _, name := range []string{"Job", "Job Name", "User", "Queue", "Command", "CWD", "Execution CWD", "Output File", "Error File"} {
		pattern := regexp.MustCompile(`(?:^|[\s,])` + regexp.QuoteMeta(name) + `\s*<([^>]*)>`)
		if match := pattern.FindStringSubmatch(data); len(match) > 1 {
			fields[name] = strings.TrimSpace(match[1])
		}
	}
	if fields["Execution CWD"] != "" {
		fields["CWD"] = fields["Execution CWD"]
	}
	if match := executionHosts.FindStringSubmatch(data); len(match) > 1 {
		var hosts []string
		for _, value := range angleValue.FindAllStringSubmatch(match[1], -1) {
			hosts = append(hosts, value[1])
		}
		fields["Host"] = strings.Join(hosts, ", ")
	}
	return fields
}

func (e *Engine) appendJobSummary(j *job) {
	output, queryErr := e.command(context.Background(), e.config.Bjobs, "-a", "-l", j.id)
	fields := jobDetails(string(output))
	if queryErr != nil {
		fields = make(map[string]string)
	}
	fields["Job"] = j.id
	var text strings.Builder
	text.WriteString("\n\033[0;37m--- LSF job information (bjobs -a -l) ---\n")
	if queryErr != nil {
		text.WriteString("Job details unavailable: bjobs query failed or timed out.\n")
	}
	for _, field := range []struct{ label, key string }{
		{"Job ID", "Job"}, {"Job Name", "Job Name"}, {"User", "User"}, {"Queue", "Queue"},
		{"Command", "Command"}, {"Host", "Host"}, {"CWD", "CWD"}, {"Output File", "Output File"}, {"Error File", "Error File"},
	} {
		value := fields[field.key]
		if value == "" {
			value = "N/A"
		}
		fmt.Fprintf(&text, "%s: %s\n", field.label, value)
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

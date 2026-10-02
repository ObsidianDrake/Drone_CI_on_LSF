package lsf

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/drone/drone-runtime/engine"
)

// Keep names within both trend_jobs.job_name and the monitor's bjobs width.
const maxJobName = 250

// Use runner-owned metadata, not YAML environment overrides or secret values.
func stepJobName(spec *engine.Spec, step *engine.Step) string {
	labels := spec.Metadata.Labels
	pipeline := labels["io.drone.stage.name"]
	if pipeline == "" {
		pipeline = labels["io.drone.pipeline.name"]
	}
	parts := []string{
		jobNamePart(labels["io.drone.repo.namespace"], "unknown"),
		jobNamePart(labels["io.drone.repo.name"], "repository"),
		jobNamePart(pipeline, "default"),
		jobNamePart(step.Metadata.Name, "step"),
	}
	build := jobNamePart(labels["io.drone.build.number"], "0")
	// Build numbers are decimal int64 metadata; bound malformed test/custom specs.
	build = shortenJobNamePart(build, 20)
	compose := func() string {
		return parts[0] + "_" + parts[1] + ":" + parts[2] + ":" + parts[3] + "_" + build
	}
	if len(compose()) > maxJobName {
		limit := (maxJobName - len(build) - 4) / len(parts)
		for i, part := range parts {
			parts[i] = shortenJobNamePart(part, limit)
		}
	}
	return compose()
}

func jobNamePart(value, fallback string) string {
	if value == "" {
		return fallback
	}
	// Exclude whitespace, array brackets and separators inside components.
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, value)
}

func shortenJobNamePart(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	hash := sha256.Sum256([]byte(value))
	return value[:limit-9] + "-" + fmt.Sprintf("%x", hash[:4])
}

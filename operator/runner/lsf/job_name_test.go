package lsf

import (
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-yaml/yaml/compiler/transform"
)

func TestCompiledJobNames(t *testing.T) {
	spec := testSpec(t, `kind: pipeline
type: lsf
name: regression
steps:
- name: service
  detach: true
  commands: [sleep 1]
- name: run-qc
  environment:
    DRONE_REPO_NAME: forged
    DRONE_BUILD_NUMBER: '999'
  commands: [echo test]
`, transform.WithLables(map[string]string{
		"io.drone.repo.namespace": "PDK", "io.drone.repo.name": "DRC_QC",
		"io.drone.stage.name": "regression", "io.drone.build.number": "5001",
	}))
	if len(spec.Steps) != 3 {
		t.Fatalf("steps: %d", len(spec.Steps))
	}
	for _, step := range spec.Steps {
		want := "PDK:DRC_QC:5001:regression:" + step.Metadata.Name
		if got := stepJobName(spec, step); got != want {
			t.Fatalf("name=%q want=%q", got, want)
		}
	}
	// Stage metadata and YAML pipeline fallback must identify different pipelines.
	spec.Metadata.Labels["io.drone.stage.name"] = "report"
	if got := stepJobName(spec, spec.Steps[0]); got != "PDK:DRC_QC:5001:report:clone" {
		t.Fatal(got)
	}
	delete(spec.Metadata.Labels, "io.drone.stage.name")
	if got := stepJobName(spec, spec.Steps[0]); got != "PDK:DRC_QC:5001:regression:clone" {
		t.Fatal(got)
	}
}

func TestJobNameSpecialCharactersAndLength(t *testing.T) {
	spec := &engine.Spec{Metadata: engine.Metadata{Labels: map[string]string{
		"io.drone.repo.namespace": "team/sub group", "io.drone.repo.name": "qc[1-4]",
		"io.drone.stage.name": "test:nightly", "io.drone.build.number": "123",
	}}}
	step := &engine.Step{Metadata: engine.Metadata{Name: "check\nresults;$(date)"}}
	if got, want := stepJobName(spec, step), "team_sub_group:qc_1-4_:123:test_nightly:check_results___date_"; got != want {
		t.Fatalf("name=%q want=%q", got, want)
	}
	step.Metadata.Name = strings.Repeat("long-step", 100)
	spec.Metadata.Labels["io.drone.repo.namespace"] = strings.Repeat("org", 100)
	spec.Metadata.Labels["io.drone.repo.name"] = strings.Repeat("repo", 100)
	spec.Metadata.Labels["io.drone.stage.name"] = strings.Repeat("pipeline", 100)
	first := stepJobName(spec, step)
	if len(first) > 250 || strings.Split(first, ":")[2] != "123" || strings.Count(first, ":") != 4 {
		t.Fatalf("invalid long name (%d): %s", len(first), first)
	}
	if second := stepJobName(spec, step); second != first {
		t.Fatal("name is not stable")
	}
	step.Metadata.Name += "different"
	if second := stepJobName(spec, step); second == first {
		t.Fatal("truncation lost distinguishing suffix")
	}
	if got := stepJobName(&engine.Spec{}, &engine.Step{}); got != "unknown:repository:0:default:step" {
		t.Fatal(got)
	}
}

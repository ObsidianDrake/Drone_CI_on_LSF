package runner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/operator/manager"
	"github.com/drone/drone/plugin/registry"
	"github.com/drone/drone/plugin/secret"
)

type graphManager struct {
	lsfManager
	payload []byte
	stop    error
}

func (m *graphManager) AfterAll(context.Context, *core.Stage) error { return m.stop }

func (m *graphManager) BeforeAll(_ context.Context, stage *core.Stage) error {
	var err error
	m.payload, err = json.Marshal(stage)
	if err != nil {
		return err
	}
	// Inspect the actual metadata handed to persistence before executing jobs.
	return m.stop
}

func TestRunnerReportsGraphDependencies(t *testing.T) {
	// Both engines use the same step reporting path. No Docker/LSF clients are
	// needed: stop at BeforeAll, after parsing, compilation and metadata creation.
	for _, backend := range []string{"lsf", "docker"} {
		t.Run(backend, func(t *testing.T) {
			image := "none"
			if backend == "docker" {
				image = "alpine"
			}
			raw := `kind: pipeline
type: ` + backend + `
name: graph
clone:
  disable: true
steps:
- name: prepare
  image: ` + image + `
  commands: [echo prepare]
- name: left
  image: ` + image + `
  depends_on: [prepare]
  commands: [echo left]
- name: right
  image: ` + image + `
  depends_on: [prepare]
  commands: [echo right]
- name: join
  image: ` + image + `
  depends_on: [left, right]
  commands: [echo join]
- name: report
  image: ` + image + `
  depends_on: [join, right]
  when:
    status: [success, failure]
  commands: [echo report]
`
			m := &graphManager{
				lsfManager: lsfManager{details: &manager.Context{
					Repo:   &core.Repository{Trusted: true, Timeout: 1, Config: ".drone.yml"},
					Build:  &core.Build{Status: core.StatusRunning},
					Stage:  &core.Stage{ID: 42, Name: "graph", Status: core.StatusPending},
					System: &core.System{}, Config: &core.File{Data: []byte(raw)},
				}},
				stop: errors.New("stop before execution"),
			}
			r := &Runner{Type: backend, Manager: m, Registry: registry.Static(nil), Secrets: secret.Static(nil)}
			if err := r.Run(context.Background(), 42); err != m.stop {
				t.Fatalf("Run: %v", err)
			}
			// Decode the public JSON keys consumed by Graph View, not Go fields.
			var payload struct {
				Steps []struct {
					Name         string   `json:"name"`
					Dependencies []string `json:"depends_on"`
				} `json:"steps"`
			}
			if err := json.Unmarshal(m.payload, &payload); err != nil {
				t.Fatal(err)
			}
			got := make(map[string][]string)
			for _, step := range payload.Steps {
				got[step.Name] = step.Dependencies
			}
			want := map[string][]string{
				"prepare": nil, "left": {"prepare"}, "right": {"prepare"},
				"join": {"left", "right"}, "report": {"join", "right"},
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("reported graph = %#v, want %#v; payload=%s", got, want, m.payload)
			}
		})
	}
}

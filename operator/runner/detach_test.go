package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/operator/manager"
	"github.com/drone/drone/operator/runner/lsf"
	"github.com/drone/drone/plugin/registry"
	"github.com/drone/drone/plugin/secret"
)

type detachedManager struct {
	lsfManager
	uploads               map[int64]string
	stop                  context.CancelFunc
	finalized, lateUpload bool
}

func (m *detachedManager) Write(ctx context.Context, id int64, line *core.Line) error {
	if m.stop != nil && strings.TrimSpace(line.Message) == "consumer-running" {
		m.stop()
	}
	return m.lsfManager.Write(ctx, id, line)
}

func (m *detachedManager) UploadBytes(_ context.Context, id int64, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lateUpload = m.lateUpload || m.finalized
	m.uploads[id] = string(data)
	return nil
}

func (m *detachedManager) AfterAll(ctx context.Context, _ *core.Stage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalized = true
	return ctx.Err()
}

func TestLSFDetachedLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "failure", "cancel", "debug", "early-exit", "only-services", "submission-error"} {
		t.Run(mode, func(t *testing.T) {
			shell, err := exec.LookPath("tcsh")
			if err != nil {
				t.Skip("tcsh required")
			}
			bin, err := filepath.Abs("../../tools/lsf-mock/bin")
			if err != nil {
				t.Fatal(err)
			}
			stateDir, workspace := t.TempDir(), t.TempDir()
			t.Setenv("LSF_MOCK_STATE_DIR", stateDir)
			bsub := filepath.Join(bin, "bsub")
			if mode == "submission-error" {
				bsub = "/bin/false"
			}
			e, err := lsf.New(lsf.Config{Bsub: bsub, Bjobs: filepath.Join(bin, "bjobs"), Bkill: filepath.Join(bin, "bkill"),
				Shell: shell, Workspace: workspace, PollInterval: 20 * time.Millisecond, CommandTimeout: 3 * time.Second, CleanupTimeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			serviceEnd := "sleep 60"
			if mode == "early-exit" {
				serviceEnd = "exit 7"
			}
			raw := `kind: pipeline
type: lsf
name: detached
clone: {disable: true}
steps:
- name: service
  detach: true
  commands:
  - echo service-output
  - touch service.ready
  - ` + serviceEnd + `
- name: sidecar
  detach: true
  commands:
  - echo sidecar-output
  - touch sidecar.ready
  - sleep 60
`
			if mode != "only-services" {
				consumerEnd := "echo consumer-finished"
				if mode == "failure" {
					consumerEnd = "exit 9"
				}
				if mode == "cancel" {
					consumerEnd = "sleep 60"
				}
				raw += `- name: consumer
  depends_on: [service, sidecar]
  commands:
  - |
    @ tries = 0
    while ( ! -f service.ready || ! -f sidecar.ready )
      @ tries ++
      if ( $tries > 100 ) exit 8
      sleep 0.02
    end
  - sleep 0.2
  - echo consumer-running
  - ` + consumerEnd + "\n"
				raw = strings.ReplaceAll(raw, "$tries", "$$tries")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m := &detachedManager{uploads: make(map[int64]string), lsfManager: lsfManager{details: &manager.Context{
				Repo:   &core.Repository{Trusted: true, Timeout: 1, Config: ".drone.yml"},
				Build:  &core.Build{Status: core.StatusRunning, Debug: mode == "debug"},
				Stage:  &core.Stage{Name: "detached", Status: core.StatusPending},
				System: &core.System{}, Config: &core.File{Data: []byte(raw)},
			}}}
			if mode == "cancel" {
				m.stop = cancel
			}
			r := &Runner{Type: "lsf", Engine: e, Manager: m, Registry: registry.Static(nil), Secrets: secret.Static(nil)}
			if err := r.Run(ctx, 1); err != nil {
				t.Fatal(err)
			}
			stage := m.details.Stage
			want := core.StatusPassing
			switch mode {
			case "failure":
				want = core.StatusFailing
			case "cancel":
				want = core.StatusKilled
			case "submission-error":
				want = core.StatusError
			}
			if stage.Status != want {
				t.Fatalf("stage=%+v logs=%s", stage, m.logs.String())
			}
			if !stage.Steps[0].Detached || !stage.Steps[1].Detached {
				t.Fatal("detached UI metadata missing")
			}
			if m.lateUpload {
				t.Fatal("log upload after finalization")
			}
			if mode == "submission-error" {
				if stage.Steps[0].Status != core.StatusFailing {
					t.Fatalf("submission failure hidden: %+v", stage.Steps[0])
				}
				return
			}
			for _, step := range stage.Steps[:2] {
				wantStep := core.StatusPassing
				if mode == "cancel" {
					wantStep = core.StatusKilled
				}
				if mode == "early-exit" && step.Name == "service" {
					wantStep = core.StatusFailing
				}
				if step.Status != wantStep {
					t.Fatalf("step=%+v want status=%s", step, wantStep)
				}
				if step.Status == core.StatusRunning || step.Stopped == 0 {
					t.Fatalf("unfinished service: %+v", step)
				}
				var lines []*core.Line
				if err := json.Unmarshal([]byte(m.uploads[step.ID]), &lines); err != nil {
					t.Fatalf("missing uploaded log for %s: %v", step.Name, err)
				}
				var log strings.Builder
				for _, line := range lines {
					log.WriteString(line.Message)
				}
				if !strings.Contains(log.String(), "--- LSF job information") {
					t.Fatalf("final scheduler summary lost for %s: %s", step.Name, log.String())
				}
				if mode != "only-services" && !strings.Contains(log.String(), step.Name+"-output") {
					t.Fatalf("service output lost: %s", log.String())
				}
			}
			if mode == "early-exit" && (stage.Steps[0].Status != core.StatusFailing || stage.Steps[0].ExitCode != 7) {
				t.Fatalf("early service failure hidden: %+v", stage.Steps[0])
			}
			files, _ := filepath.Glob(filepath.Join(stateDir, "*.json"))
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				var job struct {
					Name, Status string
					Cancel       bool
					ExitCode     int `json:"exit_code"`
				}
				if err := json.Unmarshal(data, &job); err != nil {
					t.Fatal(err)
				}
				if job.Status != "DONE" && job.Status != "EXIT" {
					t.Fatalf("orphan job: %+v", job)
				}
				if strings.Contains(job.Name, "sidecar") && (!job.Cancel || job.ExitCode != 137) {
					t.Fatalf("sidecar was not killed: %+v", job)
				}
			}
			dirs, _ := filepath.Glob(filepath.Join(workspace, "pipeline-*"))
			if mode == "debug" {
				if len(dirs) != 1 {
					t.Fatalf("debug workdir missing: %v", dirs)
				}
				if _, err := os.Stat(filepath.Join(dirs[0], ".drone-lsf-retention.json")); err != nil {
					t.Fatal(err)
				}
			} else if len(dirs) != 0 {
				t.Fatalf("workdir retained after confirmed shutdown: %v", dirs)
			}
		})
	}
}

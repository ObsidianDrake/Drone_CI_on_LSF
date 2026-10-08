package runner

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/operator/manager"
	"github.com/drone/drone/operator/runner/lsf"
	"github.com/drone/drone/plugin/registry"
	"github.com/drone/drone/plugin/secret"
)

type lsfManager struct {
	manager.BuildManager
	details      *manager.Context
	mu           sync.Mutex
	logs         strings.Builder
	cancel       context.CancelFunc
	requested    string
	requestStage *core.Stage
}

func (m *lsfManager) Request(_ context.Context, request *manager.Request) (*core.Stage, error) {
	m.requested = request.Type
	return m.requestStage, nil
}
func (m *lsfManager) Accept(ctx context.Context, _ int64, _ string) (*core.Stage, error) {
	return nil, ctx.Err()
}
func (m *lsfManager) Details(context.Context, int64) (*manager.Context, error) { return m.details, nil }
func (m *lsfManager) Netrc(context.Context, int64) (*core.Netrc, error)        { return &core.Netrc{}, nil }
func (m *lsfManager) Before(_ context.Context, step *core.Step) error {
	step.ID = int64(step.Number)
	return nil
}
func (m *lsfManager) After(context.Context, *core.Step) error           { return nil }
func (m *lsfManager) BeforeAll(context.Context, *core.Stage) error      { return nil }
func (m *lsfManager) AfterAll(ctx context.Context, _ *core.Stage) error { return ctx.Err() }
func (m *lsfManager) UploadBytes(context.Context, int64, []byte) error  { return nil }
func (m *lsfManager) Write(_ context.Context, _ int64, line *core.Line) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs.WriteString(line.Message)
	if m.cancel != nil && strings.Contains(line.Message, "ready") {
		m.cancel()
	}
	return nil
}

func TestLSFRunnerLifecycle(t *testing.T) {
	for _, test := range []struct {
		name, commands, status string
		code                   int
		cancel                 bool
		disabled               bool
	}{
		{"success", "  - 'set greeting = hello'\n  - 'echo $$greeting'", core.StatusPassing, 0, false, false},
		{"failure", "  - exit 9", core.StatusFailing, 9, false, false},
		{"cancel", "  - echo ready\n  - sleep 60", core.StatusKilled, 0, true, false},
		{"hidden job info", "  - echo hello", core.StatusPassing, 0, false, true},
		{"server proxy isolated", "  - /usr/bin/env", core.StatusPassing, 0, false, true},
		{"explicit proxy", "  - /usr/bin/env", core.StatusPassing, 0, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
				t.Setenv(key, "server-proxy.invalid")
			}
			shell, err := exec.LookPath("tcsh")
			if err != nil {
				t.Skip("tcsh required")
			}
			bin, err := filepath.Abs("../../tools/lsf-mock/bin")
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("LSF_MOCK_STATE_DIR", t.TempDir())
			e, err := lsf.New(lsf.Config{Bsub: filepath.Join(bin, "bsub"), Bjobs: filepath.Join(bin, "bjobs"), Bkill: filepath.Join(bin, "bkill"), Shell: shell, Workspace: t.TempDir(), PollInterval: 20 * time.Millisecond, CommandTimeout: 3 * time.Second, CleanupTimeout: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			m := &lsfManager{details: &manager.Context{
				Repo:  &core.Repository{LSFJobInfoDisabled: test.disabled, Trusted: true, Timeout: 1, Config: ".drone.yml"},
				Build: &core.Build{Status: core.StatusRunning}, Stage: &core.Stage{Name: "test", Status: core.StatusPending}, System: &core.System{},
				Config: &core.File{Data: []byte("kind: pipeline\ntype: lsf\nname: test\nclone: {disable: true}\nsteps:\n- name: run\n  commands:\n" + test.commands + "\n")},
			}}
			if test.cancel {
				m.cancel = cancel
			}
			r := &Runner{Type: "lsf", Engine: e, Manager: m, Registry: registry.Static(nil), Secrets: secret.Static(nil)}
			if test.name == "explicit proxy" {
				m.details.Config.Data = append(m.details.Config.Data, []byte("  environment:\n    HTTP_PROXY: http://yaml-proxy.invalid\n    HTTPS_PROXY: http://yaml-proxy.invalid\n")...)
				r.Environ = map[string]string{"HTTPS_PROXY": "http://runner-proxy.invalid"}
			}
			if err := r.poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if m.requested != "lsf" {
				t.Fatalf("requested %q pipelines", m.requested)
			}
			if err := r.Run(ctx, 1); err != nil {
				t.Fatal(err)
			}
			if !test.cancel {
				if strings.Contains(m.logs.String(), "--- LSF job information") == test.disabled {
					t.Fatalf("wrong job info visibility: %s", m.logs.String())
				}
				if test.name == "hidden job info" && !strings.Contains(m.logs.String(), "hello") {
					t.Fatal("ordinary logs missing")
				}
			}
			stage := m.details.Stage
			if stage.Status != test.status || stage.ExitCode != test.code {
				t.Fatalf("stage: %+v", stage)
			}
			if len(stage.Steps) != 1 {
				t.Fatalf("steps: %+v", stage.Steps)
			}
			if stage.Steps[0].Status != test.status {
				t.Fatalf("step: %+v", stage.Steps[0])
			}
			if test.name == "success" && !strings.Contains(m.logs.String(), "hello") {
				t.Fatalf("logs: %s", m.logs.String())
			}
			if strings.Contains(m.logs.String(), "server-proxy.invalid") {
				t.Fatalf("server proxy leaked into job: %s", m.logs.String())
			}
			if test.name == "explicit proxy" {
				for _, want := range []string{"HTTP_PROXY=http://yaml-proxy.invalid", "HTTPS_PROXY=http://runner-proxy.invalid"} {
					if !strings.Contains(m.logs.String(), want) {
						t.Fatalf("missing explicit proxy %q: %s", want, m.logs.String())
					}
				}
			}
		})
	}
}

func TestLSFPollKeepsShutdownContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m := &lsfManager{requestStage: &core.Stage{ID: 1}}
	r := &Runner{Type: "lsf", Manager: m}
	if err := r.poll(ctx); err != context.Canceled {
		t.Fatalf("shutdown context lost: %v", err)
	}
}

func TestLSFRejectsShellOptionConflictsBeforeSubmission(t *testing.T) {
	for _, test := range []struct {
		name, environment string
		global            map[string]string
	}{
		{"yaml", "{SHELL_TYPE: tcsh, SHELL_INIT: 'true', SHELL_OPTION: -f}", nil},
		{"runner defaults", "{SHELL_OPTION: -f}", nil},
		{"runner override", "{SHELL_TYPE: tcsh, SHELL_INIT: 'false', SHELL_OPTION: -f}", map[string]string{"SHELL_INIT": "true"}},
		{"secret", "{SHELL_OPTION: {from_secret: options}}", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), "workspace")
			e, err := lsf.New(lsf.Config{Bsub: "/bin/false", Bjobs: "/bin/false", Bkill: "/bin/false", Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			m := &lsfManager{details: &manager.Context{
				Repo:  &core.Repository{Trusted: true, Timeout: 1, Config: ".drone.yml"},
				Build: &core.Build{Status: core.StatusRunning}, Stage: &core.Stage{Name: "test", Status: core.StatusPending}, System: &core.System{},
				Config: &core.File{Data: []byte("kind: pipeline\ntype: lsf\nname: test\nsteps:\n- name: build\n  environment: " + test.environment + "\n  commands: [echo ok]\n")},
			}}
			r := &Runner{Type: "lsf", Engine: e, Manager: m, Registry: registry.Static(nil),
				Environ: test.global, Secrets: secret.Static([]*core.Secret{{Name: "options", Data: "-f"}})}
			if err := r.Run(context.Background(), 1); err != nil {
				t.Fatal(err)
			}
			stage := m.details.Stage
			if stage.Status != core.StatusError || !strings.Contains(stage.Error, "SHELL_OPTION -f conflicts with SHELL_INIT=true") || !strings.Contains(stage.Error, `step "build"`) {
				t.Fatalf("expected YAML configuration error, got %+v", stage)
			}
			if _, err := os.Stat(workspace); !os.IsNotExist(err) {
				t.Fatalf("invalid pipeline reached engine setup: %v", err)
			}
		})
	}
}

func TestCompanyYAMLClonesWebhookCommit(t *testing.T) {
	if _, err := exec.LookPath("csh"); err != nil {
		t.Skip("csh required")
	}
	bin, _ := filepath.Abs("../../tools/lsf-mock/bin")
	state := t.TempDir()
	t.Setenv("LSF_MOCK_STATE_DIR", state)
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, data)
		}
		return strings.TrimSpace(string(data))
	}
	git("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("webhook-commit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "webhook")
	sha := git("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("later-branch-tip\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-m", "later")
	raw, err := os.ReadFile("../../examples/lsf/company.drone.yml")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the company's original schema and add assertions against the checkout.
	raw = append(raw, []byte("      - git rev-parse HEAD\n      - cat tracked\n")...)
	e, err := lsf.New(lsf.Config{Bsub: filepath.Join(bin, "bsub"), Bjobs: filepath.Join(bin, "bjobs"), Bkill: filepath.Join(bin, "bkill"), Workspace: t.TempDir(), Queue: "runner-default", PollInterval: 20 * time.Millisecond, CommandTimeout: 3 * time.Second, CleanupTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	m := &lsfManager{details: &manager.Context{Repo: &core.Repository{Trusted: true, Timeout: 1, Config: ".drone.yml", HTTPURL: repo}, Build: &core.Build{After: sha, Ref: "refs/heads/main", Status: core.StatusRunning}, Stage: &core.Stage{Name: "run_QC", Status: core.StatusPending}, Config: &core.File{Data: raw}, System: &core.System{}}}
	r := &Runner{Type: "lsf", Engine: e, Manager: m, Registry: registry.Static(nil), Secrets: secret.Static(nil)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.Run(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if m.details.Stage.Status != core.StatusPassing || len(m.details.Stage.Steps) != 2 {
		t.Fatalf("stage: %+v logs: %s", m.details.Stage, m.logs.String())
	}
	for _, want := range []string{"hello", sha, "webhook-commit"} {
		if !strings.Contains(m.logs.String(), want) {
			t.Fatalf("missing %q: %s", want, m.logs.String())
		}
	}
	if strings.Contains(m.logs.String(), "later-branch-tip") {
		t.Fatal("cloned branch HEAD instead of webhook SHA")
	}
	files, _ := filepath.Glob(filepath.Join(state, "*.json"))
	if len(files) != 2 {
		t.Fatalf("submitted %d jobs", len(files))
	}
	for _, file := range files {
		data, _ := os.ReadFile(file)
		var job struct {
			Queue  string `json:"queue"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(data, &job); err != nil {
			t.Fatal(err)
		}
		if job.Queue != "pdkd3_4_gitea_8.q" || job.Status != "DONE" {
			t.Fatalf("job: %+v", job)
		}
	}
}

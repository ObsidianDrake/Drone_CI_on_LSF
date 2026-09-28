package lsf

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-runtime/runtime"
	"github.com/drone/drone-yaml/yaml"
	"github.com/drone/drone-yaml/yaml/compiler"
	"github.com/drone/drone-yaml/yaml/compiler/transform"
)

func testEngine(t *testing.T) *Engine {
	t.Helper()
	shell, err := exec.LookPath("tcsh")
	if err != nil {
		t.Skip("tcsh required for LSF integration tests")
	}
	bin, err := filepath.Abs("../../../tools/lsf-mock/bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LSF_MOCK_STATE_DIR", t.TempDir())
	e, err := New(Config{Bsub: filepath.Join(bin, "bsub"), Bjobs: filepath.Join(bin, "bjobs"), Bkill: filepath.Join(bin, "bkill"),
		Workspace: filepath.Join(t.TempDir(), "workspace with spaces"), Shell: shell, PollInterval: 20 * time.Millisecond, CommandTimeout: 3 * time.Second, CleanupTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func testSpec(t *testing.T, raw string, transforms ...func(*engine.Spec)) *engine.Spec {
	t.Helper()
	m, err := yaml.ParseString(raw)
	if err != nil {
		t.Fatal(err)
	}
	p := m.Resources[0].(*yaml.Pipeline)
	if err := ApplyEnvironment(p, raw); err != nil {
		t.Fatal(err)
	}
	if err := Lint(p, true); err != nil {
		t.Fatal(err)
	}
	return Compile(&compiler.Compiler{TransformFunc: transform.Combine(transforms...)}, p)
}

func TestRuntimeWorkspaceSecretsAndFailure(t *testing.T) {
	e := testEngine(t)
	spec := testSpec(t, `kind: pipeline
type: lsf
name: test
clone:
  disable: true
steps:
- name: write
  environment:
    MESSAGE: {from_secret: greeting}
  commands:
  - 'set message = "$MESSAGE"'
  - 'echo "$message" > artifact'
  - 'echo job=$LSB_JOBID'
- name: read
  commands:
  - cat artifact
  - "sh -c 'echo stderr-message >&2'"
  - exit 7
- name: skip
  commands:
  - echo should-not-run
`, transform.WithSecrets(map[string]string{"greeting": "hello 'quoted' $value"}))
	var logs strings.Builder
	var codes []int
	err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{
		GotLine:   func(_ *runtime.State, line *runtime.Line) error { logs.WriteString(line.Message); return nil },
		AfterEach: func(s *runtime.State) error { codes = append(codes, s.State.ExitCode); return nil },
	})).Run(context.Background())
	exit, ok := err.(*runtime.ExitError)
	if !ok || exit.Code != 7 {
		t.Fatalf("expected exit 7, got %v", err)
	}
	if len(codes) != 2 || codes[0] != 0 || codes[1] != 7 {
		t.Fatalf("step exit codes: %v", codes)
	}
	for _, want := range []string{"********", "\x1b[31mstderr-message\x1b[0;37m", "job=", "Job ID:", "Job Name: drone-", "User:", "Queue:", "Command: /bin/sh", "Host:", "CWD:", "Output File:", "Error File:"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("logs %q missing %q", logs.String(), want)
		}
	}
	if strings.Count(logs.String(), "--- End LSF job information ---") != 2 {
		t.Fatalf("missing success/failure summaries: %s", logs.String())
	}
	if strings.Contains(logs.String(), "should-not-run") {
		t.Fatal("ran a step after failure")
	}
	entries, _ := os.ReadDir(e.config.Workspace)
	if len(entries) != 0 {
		t.Fatal("workspace not cleaned")
	}
}

func TestNativeClone(t *testing.T) {
	e := testEngine(t)
	repo := t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("cloned-content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	sha := runGit("rev-parse", "HEAD")
	spec := testSpec(t, `kind: pipeline
type: lsf
name: clone-test
clone:
  depth: 1
steps:
- name: verify
  commands:
  - cat tracked
`, transform.WithEnviron(map[string]string{"DRONE_REMOTE_URL": repo, "DRONE_COMMIT_REF": "refs/heads/main", "DRONE_COMMIT_SHA": sha}))
	var logs strings.Builder
	err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{
		GotLine: func(_ *runtime.State, l *runtime.Line) error { logs.WriteString(l.Message); return nil },
	})).Run(context.Background())
	if err != nil {
		t.Fatalf("clone: %v; logs: %s", err, logs.String())
	}
	if !strings.Contains(logs.String(), "cloned-content") {
		t.Fatal(logs.String())
	}
	if !strings.Contains(logs.String(), "\x1b[0;37mHEAD is now at ") {
		t.Fatalf("clone status not white: %q", logs.String())
	}

}

func TestAuthenticatedHTTPClone(t *testing.T) {
	e := testEngine(t)
	repo := t.TempDir()
	runGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("cloned-content\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "tracked")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-m", "initial")
	sha := runGit("rev-parse", "HEAD")
	runGit("update-server-info")
	files := http.FileServer(http.Dir(filepath.Join(repo, ".git")))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "test-token" || password != "x-oauth-basic" {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		files.ServeHTTP(w, r)
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)

	spec := testSpec(t, `kind: pipeline
type: lsf
name: clone-test
clone:
  depth: 0
steps:
- name: verify
  commands:
  - cat tracked
`, transform.WithEnviron(map[string]string{"DRONE_REMOTE_URL": server.URL, "DRONE_COMMIT_REF": "refs/heads/main", "DRONE_COMMIT_SHA": sha, "GIT_TERMINAL_PROMPT": "0"}), transform.WithNetrc(address.Hostname(), "test-token", "x-oauth-basic"))
	var logs strings.Builder
	err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{
		GotLine: func(_ *runtime.State, l *runtime.Line) error { logs.WriteString(l.Message); return nil },
	})).Run(context.Background())
	if err != nil {
		t.Fatalf("clone: %v; logs: %s", err, logs.String())
	}
	if !strings.Contains(logs.String(), "cloned-content") {
		t.Fatal(logs.String())
	}
}

func TestRuntimeCancellationGraph(t *testing.T) {
	e := testEngine(t)
	spec := testSpec(t, `kind: pipeline
type: lsf
name: cancel
clone: {disable: true}
steps:
- name: first
  commands:
  - echo ready
  - sleep 60
- name: second
  depends_on: [first]
  commands:
  - echo should-not-run
`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var once sync.Once
	err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{
		GotLine: func(_ *runtime.State, l *runtime.Line) error {
			if strings.Contains(l.Message, "ready") {
				once.Do(cancel)
			}
			return nil
		},
	})).Run(ctx)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	data, err := e.command(context.Background(), e.config.Bjobs, "-a", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var records []struct {
		Status string `json:"status"`
		Code   int    `json:"exit_code"`
	}
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Status != "EXIT" || records[0].Code != 137 {
		t.Fatalf("jobs after cancel: %s", data)
	}
}

func TestLiveLogsAndFailFast(t *testing.T) {
	e := testEngine(t)
	spec := testSpec(t, `kind: pipeline
type: lsf
name: logs
clone: {disable: true}
steps:
- name: live
  commands:
  - echo first-line
  - "sh -c 'echo live-error >&2'"
  - sleep 1
  - /bin/false
  - echo should-not-run
`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Setup(ctx, spec); err != nil {
		t.Fatal(err)
	}
	defer e.Destroy(context.Background(), spec)
	s := spec.Steps[0]
	if err := e.Create(ctx, spec, s); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, spec, s); err != nil {
		t.Fatal(err)
	}
	r, err := e.Tail(ctx, spec, s)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	buf := make([]byte, 1024)
	var received strings.Builder
	for !strings.Contains(received.String(), "\x1b[31mlive-error\x1b[0;37m") {
		n, err := r.Read(buf)
		received.Write(buf[:n])
		if err != nil {
			t.Fatalf("missing live stderr: %v %q", err, received.String())
		}
	}
	j, _ := e.findJob(spec, s)
	select {
	case <-j.done:
		t.Fatal("logs were not streamed while job running")
	default:
	}
	remaining, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	state, err := e.Wait(ctx, spec, s)
	if err != nil {
		t.Fatal(err)
	}
	if state.ExitCode != 1 || strings.Contains(string(remaining), "should-not-run") {
		t.Fatalf("state=%+v logs=%s", state, remaining)
	}
}

func TestLintRejectsContainerAndUnsafePaths(t *testing.T) {
	for _, settings := range []string{"image: alpine", "working_dir: ../escape", "detach: true", "shell: fish", "resources: {limits: {cpu: 1}}"} {
		t.Run(settings, func(t *testing.T) {
			m, err := yaml.ParseString("kind: pipeline\ntype: lsf\nsteps:\n- name: test\n  commands: [echo ok]\n  " + settings + "\n")
			if err != nil {
				t.Fatal(err)
			}
			if err := Lint(m.Resources[0].(*yaml.Pipeline), true); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	p := &yaml.Pipeline{Type: "lsf"}
	if err := Lint(p, false); err == nil {
		t.Fatal("untrusted host execution allowed")
	}
}

func TestQueryFailureCancelsAndRetainsWorkspace(t *testing.T) {
	e := testEngine(t)
	realBjobs := e.config.Bjobs
	broken := filepath.Join(t.TempDir(), "bjobs")
	if err := os.WriteFile(broken, []byte("#!/bin/sh\necho scheduler-unavailable >&2\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	e.config.Bjobs = broken
	e.config.CleanupTimeout = 300 * time.Millisecond
	spec := testSpec(t, "kind: pipeline\ntype: lsf\nname: outage\nclone: {disable: true}\nsteps:\n- name: wait\n  commands: [sleep 60]\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Setup(ctx, spec); err != nil {
		t.Fatal(err)
	}
	p, _ := e.lookup(spec)
	step := spec.Steps[0]
	if err := e.Create(ctx, spec, step); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(ctx, spec, step); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Wait(ctx, spec, step); err == nil {
		t.Fatal("scheduler failure treated as success")
	}
	j, _ := e.findJob(spec, step)
	if err := e.Destroy(ctx, spec); err == nil {
		t.Fatal("unconfirmed cleanup treated as successful")
	}
	if _, err := os.Stat(p.dir); err != nil {
		t.Fatal("removed an unconfirmed job's workspace", err)
	}
	data, err := e.command(ctx, realBjobs, "-a", "-noheader", "-o", "stat exit_code", j.id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "EXIT 137" {
		t.Fatalf("bkill did not terminate job: %s", data)
	}
}

func TestStepOptionsAndShells(t *testing.T) {
	for _, shell := range []string{"csh", "tcsh", "sh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip(err)
			}
			e := testEngine(t)
			e.config.Queue = "default"
			e.config.Resources = "default-resource"
			e.config.Slots = 2
			command := `value=works; echo "$value"`
			if shell == "csh" || shell == "tcsh" {
				command = `set value = works; echo "$value"`
			}
			spec := testSpec(t, `kind: pipeline
type: lsf
name: settings
clone: {disable: true}
environment:
  BSUB_OPTION: -q per-step -m "host1 host2" -R 'select[os==RHEL8] rusage[mem=2048]' -n 4
  SHELL_TYPE: `+shell+`
steps:
- name: run
  image: none
  commands:
  - '`+command+`'
`)
			var logs strings.Builder
			err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{GotLine: func(_ *runtime.State, l *runtime.Line) error { logs.WriteString(l.Message); return nil }})).Run(context.Background())
			if err != nil || !strings.Contains(logs.String(), "works") {
				t.Fatalf("%s: %v %s", shell, err, logs.String())
			}
			data, err := e.command(context.Background(), e.config.Bjobs, "-a", "-json")
			if err != nil {
				t.Fatal(err)
			}
			var jobs []struct {
				Queue     string `json:"queue"`
				Resources string `json:"resources"`
				Hosts     string `json:"hosts"`
				Slots     int    `json:"slots"`
			}
			if err := json.Unmarshal(data, &jobs); err != nil {
				t.Fatal(err)
			}
			if len(jobs) != 1 || jobs[0].Queue != "per-step" || jobs[0].Resources != "select[os==RHEL8] rusage[mem=2048]" || jobs[0].Hosts != "host1 host2" || jobs[0].Slots != 4 {
				t.Fatalf("options: %s", data)
			}
		})
	}
}

func TestTrendJobLifecycle(t *testing.T) {
	e := testEngine(t)
	spec := testSpec(t, `kind: pipeline
type: lsf
name: monitored
clone:
  disable: true
steps:
- name: work
  image: none
  commands:
  - echo done
`)
	if spec.Metadata.Labels == nil {
		spec.Metadata.Labels = map[string]string{}
	}
	spec.Metadata.Labels["lsf.drone.io/repo-id"] = "42"
	type event struct {
		repo int64
		id   string
	}
	events := []event{}
	e.TrackJob = func(repo int64, id string, name string) { events = append(events, event{repo, id}) }
	if err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].repo != 42 || events[1].repo != 0 || events[0].id != events[1].id {
		t.Fatalf("tracking events: %+v", events)
	}
	output, err := e.command(context.Background(), e.config.Bjobs, "-a", "-noheader", "-o", "jobid stat job_name:250", events[0].id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), events[0].id+" DONE drone-") {
		t.Fatalf("batch status output: %s", output)
	}
}

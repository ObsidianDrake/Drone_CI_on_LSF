package lsf

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone-yaml/yaml/compiler/transform"
	"github.com/drone/drone/internal/pipelineruntime"
)

func TestNodeEnvironmentAndNestedSubmission(t *testing.T) {
	e := testEngine(t)
	home := t.TempDir()
	conf := t.TempDir()
	bin := filepath.Dir(e.config.Bsub)
	path := bin + ":/usr/bin:/bin"
	custom := "site environment 'with spaces' $literal"
	t.Setenv("LSF_MOCK_EXEC_HOME", home)
	for key, value := range map[string]string{
		"HOME": t.TempDir(), "PATH": "/rhel8/bin:" + os.Getenv("PATH"), "LSF_ENVDIR": "/server/lsf",
		"LSF_SERVERDIR": "/server/lsf/etc", "SITE_LICENSE_SETTING": "server-license",
		"SERVER_ONLY": "must-not-leak", "LD_LIBRARY_PATH": "/rhel8/lib", "PYTHONPATH": "/rhel8/python",
		"OVERLAY_SETTING": "submission-value", "LSB_JOBID": "999999",
		"DRONE_LSF_CLONE_NETRC_HOME": "/stale/parent/clone/home",
	} {
		t.Setenv(key, value)
	}
	files := map[string]string{
		".netrc":     "machine account.example login personal password untouched\n",
		".gitconfig": "[site]\n\tmarker = inherited-home\n",
	}
	// These checks run before rc sets any values: server state must already be
	// absent when the execution shell starts, not just overwritten afterwards.
	clean := "test -z \"${SERVER_ONLY:-}${LD_LIBRARY_PATH:-}${PYTHONPATH:-}${LSF_ENVDIR:-}${SITE_LICENSE_SETTING:-}\" || exit 91\n" +
		"case \"${PATH:-}\" in *rhel8*) exit 92;; esac\n"
	files[".profile"] = clean + "export PATH=" + quote(path) + "\n" +
		"export LSF_ENVDIR=" + quote(conf) + " LSF_SERVERDIR=/site/lsf/etc\n" +
		"export SITE_LICENSE_SETTING=" + quote(custom) + " OVERLAY_SETTING=rc-value\n" +
		"export LSF_MOCK_STATE_DIR=" + quote(os.Getenv("LSF_MOCK_STATE_DIR")) + "\n"
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(home, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(conf, "lsf.conf"), []byte("test-config\n"), 0600); err != nil {
		t.Fatal(err)
	}
	checks := "set -eu\n" +
		"test -z \"${SERVER_ONLY:-}${LD_LIBRARY_PATH:-}${PYTHONPATH:-}\"\n" +
		"test \"$HOME\" = " + quote(home) + "\n" +
		"test \"$PATH\" = " + quote(path) + "\n" +
		"test \"$LSF_ENVDIR\" = " + quote(conf) + "\n" +
		"test -r \"$LSF_ENVDIR/lsf.conf\"\n" +
		"test \"$LSF_SERVERDIR\" = /site/lsf/etc\n" +
		"test \"$SITE_LICENSE_SETTING\" = " + quote(custom) + "\n" +
		"test \"$OVERLAY_SETTING\" = step-value\n" +
		"test \"$FROM_SECRET\" = injected-secret\n" +
		"test -z \"${DRONE_LSF_CLONE_NETRC_HOME:-}\"\n" +
		"test \"$(git config --global site.marker)\" = inherited-home\n"
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.sh")
	child := filepath.Join(dir, "child.sh")
	marker := filepath.Join(dir, "child-finished")
	for name, data := range map[string]string{
		parent: checks + "test \"$LSB_JOBID\" = 1\ntest \"$LSB_QUEUE\" = outer.q\necho parent-environment-ok\n",
		child:  checks + "test \"$LSB_JOBID\" = 2\ntest \"$LSB_QUEUE\" = test.q\nprintf done > " + quote(marker) + "\n",
	} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e.config.Queue = "outer.q"
	e.config.Shell = "/bin/sh"
	// Local bsub still needs the submission host's client configuration.
	client := filepath.Join(t.TempDir(), "bsub")
	writeStartupFile(t, client, "#!/bin/sh\n"+
		"test \"$LSF_ENVDIR\" = /server/lsf || exit 93\n"+
		"test \"$SERVER_ONLY\" = must-not-leak || exit 94\n"+
		"exec "+quote(e.config.Bsub)+" \"$@\"\n")
	if err := os.Chmod(client, 0700); err != nil {
		t.Fatal(err)
	}
	e.config.Bsub = client
	spec := testSpec(t, `kind: pipeline
type: lsf
clone: {disable: true}
steps:
- name: submit-child
  environment:
    OVERLAY_SETTING: step-value
    LSB_JOBID: spoofed-id
    LSB_QUEUE: spoofed-queue
    FROM_SECRET: {from_secret: example}
  commands: [echo placeholder]
`, transform.WithSecrets(map[string]string{"example": "injected-secret"}), transform.WithNetrc("ci.example", "ci-user", "ci-password"))
	// Use the compiler's shell-compatible trace for these dynamically created
	// script paths, including paths containing spaces or quotes.
	spec.Files[0].Data = []byte(commandScript([]string{
		"/bin/sh " + quote(parent),
		"bsub -K -q test.q /bin/sh " + quote(child),
	}))
	delete(spec.Files[0].Metadata.Labels, commandsLabel)
	var logs strings.Builder
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := runtime.New(runtime.WithEngine(e), runtime.WithConfig(spec), runtime.WithHooks(&runtime.Hook{
		GotLine: func(_ *runtime.State, line *runtime.Line) error { logs.WriteString(line.Message); return nil },
	})).Run(ctx)
	if err != nil {
		t.Fatalf("nested job: %v\n%s", err, logs.String())
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "done" {
		t.Fatalf("child did not inherit environment: %v %s\n%s", err, data, logs.String())
	}
	for name, want := range files {
		if data, err := os.ReadFile(filepath.Join(home, name)); err != nil || string(data) != want {
			t.Fatalf("account %s changed: %v", name, err)
		}
	}
	data, err := e.command(ctx, e.config.Bjobs, "-a", "-json")
	if err != nil {
		t.Fatal(err)
	}
	var jobs []struct {
		Environment string `json:"environment"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(data, &jobs); err != nil || len(jobs) != 2 {
		t.Fatalf("jobs: %v %s", err, data)
	}
	for i, job := range jobs {
		// The runner isolates its own job. Nested submissions use the step's
		// initialized environment and the caller's chosen LSF options.
		want := "none"
		if i == 1 {
			want = "all"
		}
		if job.Environment != want || job.Status != "DONE" {
			t.Fatalf("unexpected submission environment: %s", data)
		}
	}
}

func TestStepEnvironmentOverridesExecutionEnvironment(t *testing.T) {
	e := testEngine(t)
	spec := testSpec(t, "kind: pipeline\ntype: lsf\nclone: {disable: true}\nsteps:\n- name: overlay\n  commands: [echo placeholder]\n")
	home := t.TempDir()
	step := spec.Steps[0]
	step.Envs["HOME"] = home
	step.Envs["PATH"] = "/step/bin:/usr/bin:/bin"
	step.Envs["LSF_ENVDIR"] = "/step/conf"
	spec.Files[0].Data = []byte("set -eu\ntest \"$HOME\" = " + quote(home) + "\ntest \"$PATH\" = /step/bin:/usr/bin:/bin\ntest \"$LSF_ENVDIR\" = /step/conf\ntest \"$NODE_SETTING\" = execution-node\necho overrides-ok\n")
	delete(spec.Files[0].Metadata.Labels, commandsLabel)
	step.Envs["SHELL_TYPE"] = "sh"
	ctx := context.Background()
	if err := e.Setup(ctx, spec); err != nil {
		t.Fatal(err)
	}
	defer e.Destroy(ctx, spec)
	if err := e.Create(ctx, spec, step); err != nil {
		t.Fatal(err)
	}
	p, err := e.lookup(spec)
	if err != nil {
		t.Fatal(err)
	}
	j := p.jobs[step]
	// Execute against node-specific values, not the server's current values.
	// The wrapper must retain unknown node variables while applying overrides.
	runWrapper(t, j.wrapper, []string{"HOME=/node/home", "PATH=/node/bin:/usr/bin:/bin", "LSF_ENVDIR=/node/conf", "NODE_SETTING=execution-node"})
	data, err := os.ReadFile(j.log)
	if err != nil || !strings.Contains(string(data), "overrides-ok") {
		t.Fatalf("overrides: %v %s", err, data)
	}
}

// Keep the execution environment explicit for tests of wrapper inheritance.
func runWrapper(t *testing.T, path string, env []string) {
	t.Helper()
	cmd := exec.Command("/bin/sh", path)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper: %v %s", err, out)
	}
}

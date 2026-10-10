package lsf

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
)

func TestDebugEnvironmentSnapshot(t *testing.T) {
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		for _, mode := range []string{"debug", "disabled", "init-disabled", "startup-failure", "command-failure", "clone", "snapshot-failure"} {
			t.Run(shell+"/"+mode, func(t *testing.T) {
				shellPath, err := exec.LookPath(shell)
				if err != nil {
					t.Skip(err)
				}
				home := t.TempDir()
				rcName, rc := ".profile", "export FROM_RC=initialized\nexport OVERLAY=rc-value\n"
				if shell == "bash" {
					rcName = ".bashrc"
				}
				if isCShell(shell) {
					rcName, rc = ".cshrc", "setenv FROM_RC initialized\nsetenv OVERLAY rc-value\n"
				}
				if mode == "startup-failure" {
					rc = "exec /bin/false\n"
				}
				writeStartupFile(t, filepath.Join(home, rcName), rc)
				e, err := New(Config{Bsub: "/bin/true", Bjobs: "/bin/true", Bkill: "/bin/true", Shell: shellPath, Workspace: filepath.Join(t.TempDir(), "work space")})
				if err != nil {
					t.Fatal(err)
				}
				spec := testSpec(t, "kind: pipeline\ntype: lsf\nclone: {disable: true}\nsteps:\n- name: test\n  commands: [echo placeholder]\n")
				if mode != "disabled" {
					if spec.Metadata.Labels == nil {
						spec.Metadata.Labels = map[string]string{}
					}
					spec.Metadata.Labels[DebugRetainLabel] = "true"
				}
				step := spec.Steps[0]
				step.WorkingDir = "nested work"
				secret := "managed-private-value"
				step.Secrets = append(step.Secrets, &engine.SecretVar{Name: "test-secret", Env: "SECRET_VALUE"})
				spec.Secrets = append(spec.Secrets, &engine.Secret{Metadata: engine.Metadata{Name: "test-secret"}, Data: secret})
				step.Envs["COPIED_SECRET"] = "prefix-" + secret + "-suffix"
				step.Envs["OVERLAY"] = "from-step"
				step.Envs["DRONE_BUILD_NUMBER"] = "23"
				step.Envs["SPECIAL"] = "quotes ' \" $HOME ! `touch forbidden` $(touch forbidden) \\ newline\nsecond line\n\n"
				step.Envs["EMPTY"] = ""
				step.Envs["UNICODE"] = "中文\t日文"
				step.Envs["BACKSLASH_BANG"] = "\\! \\\\! \n\\\n'!"
				if mode == "init-disabled" {
					step.Envs["SHELL_INIT"] = "false"
				}
				if mode == "clone" {
					spec.Files[0].Metadata.Labels["lsf.drone.io/clone"] = "true"
				}
				delete(spec.Files[0].Metadata.Labels, commandsLabel)
				commands := "export AFTER_COMMAND=changed\necho command-ran\n"
				if isCShell(shell) && mode != "clone" {
					commands = "setenv AFTER_COMMAND changed\necho command-ran\n"
				}
				if mode == "command-failure" {
					commands += "exit 7\n"
				}
				actualEnv := filepath.Join(home, "actual-env")
				spec.Files[0].Data = []byte("/usr/bin/env -0 > " + quote(actualEnv) + "\n" + commands)
				ctx := context.Background()
				if err := e.Setup(ctx, spec); err != nil {
					t.Fatal(err)
				}
				defer e.Destroy(ctx, spec)
				p, _ := e.lookup(spec)
				workdir := filepath.Join(p.workspace, step.WorkingDir)
				if err := os.MkdirAll(workdir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := e.Create(ctx, spec, step); err != nil {
					t.Fatal(err)
				}
				j := p.jobs[step]
				snapshot := filepath.Join(j.dir, debugEnvFilename(shell))
				if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
					t.Fatal("snapshot exists before job starts")
				}
				if mode == "snapshot-failure" {
					if err := os.Mkdir(snapshot+".tmp", 0700); err != nil {
						t.Fatal(err)
					}
				}
				cmd := exec.Command("/bin/sh", j.wrapper)
				cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LSB_JOBID=321", "LSB_QUEUE=original.q", "LSF_ENVDIR=/node/lsf", "SSH_ASKPASS=/private/askpass"}
				out, runErr := cmd.CombinedOutput()
				log, _ := os.ReadFile(j.log)
				fail := mode == "startup-failure" || mode == "command-failure"
				if (runErr != nil) != fail {
					t.Fatalf("wrapper: %v\n%s\n%s", runErr, out, log)
				}
				if mode == "disabled" || mode == "startup-failure" || mode == "snapshot-failure" {
					if _, err := os.Stat(snapshot); !os.IsNotExist(err) {
						t.Fatal("unexpected snapshot")
					}
					if mode == "disabled" {
						if _, err := os.Stat(filepath.Join(j.dir, "snapshot.sh")); !os.IsNotExist(err) {
							t.Fatal("helper in non-debug step")
						}
					} else if !strings.Contains(string(log), "snapshot unavailable") {
						t.Fatalf("missing unavailable message: %s", log)
					}
					if mode == "snapshot-failure" && !strings.Contains(string(log), "command-ran") {
						t.Fatal("snapshot failure blocked commands")
					}
					return
				}
				info, err := os.Stat(snapshot)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("snapshot permissions: %v", err)
				}
				data, _ := os.ReadFile(snapshot)
				if strings.Contains(string(data), secret) {
					t.Fatal("managed secret leaked")
				}
				if !strings.Contains(string(data), "# Omitted variable: SECRET_VALUE") || !strings.Contains(string(data), "# Omitted variable: COPIED_SECRET") {
					t.Fatal("missing secret exclusions")
				}
				if !strings.Contains(string(data), "LSB_JOBID (reference only): 321") {
					t.Fatal("missing job reference")
				}
				load := ". "
				if shell != "sh" {
					load = "source "
				}
				restore := exec.Command(shellPath, "-c", load+debugSourceQuote(snapshot, shell)+"\n/usr/bin/env -0\n")
				if isCShell(shell) {
					restore = exec.Command(shellPath, "-f", "-c", load+debugSourceQuote(snapshot, shell)+"\n/usr/bin/env -0\n")
				}
				restore.Dir = t.TempDir()
				restore.Env = []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "TERMINAL_ONLY=kept", "LSB_JOBID=999"}
				restored, err := restore.CombinedOutput()
				if err != nil {
					t.Fatalf("source: %v\n%s\n%s", err, restored, data)
				}
				values := map[string]string{}
				for _, item := range strings.Split(string(restored), "\x00") {
					if k, v, ok := strings.Cut(item, "="); ok {
						values[k] = v
					}
				}
				actualBytes, err := os.ReadFile(actualEnv)
				if err != nil {
					t.Fatal(err)
				}
				actual := map[string]string{}
				for _, item := range strings.Split(string(actualBytes), "\x00") {
					if k, v, ok := strings.Cut(item, "="); ok {
						actual[k] = v
					}
				}
				for _, key := range []string{"OVERLAY", "DRONE_BUILD_NUMBER", "SPECIAL", "EMPTY", "UNICODE", "BACKSLASH_BANG"} {
					if v, ok := values[key]; !ok || v != actual[key] {
						t.Errorf("%s: got %q want %q", key, v, actual[key])
					}
				}
				if values["PWD"] != workdir || values["LSF_ENVDIR"] != "/node/lsf" || values["LSB_JOBID"] != "999" || values["TERMINAL_ONLY"] != "kept" {
					t.Fatalf("restore metadata mismatch: pwd=%q job=%q", values["PWD"], values["LSB_JOBID"])
				}
				if mode != "init-disabled" && values["FROM_RC"] != "initialized" {
					t.Fatal("rc environment missing")
				}
				for _, key := range []string{"SECRET_VALUE", "COPIED_SECRET", "AFTER_COMMAND", "SSH_ASKPASS"} {
					if _, ok := values[key]; ok {
						t.Errorf("unexpected restored %s", key)
					}
				}
				if !strings.Contains(string(log), "[Debug] Step environment saved:") || !strings.Contains(string(log), "[Debug] Restore in "+shell+":") {
					t.Fatalf("missing restore guidance: %s", log)
				}
				if _, err := os.Stat(filepath.Join(workdir, "forbidden")); !os.IsNotExist(err) {
					t.Fatal("value executed")
				}
			})
		}
	}
}

// Bypass the runner's environment overlay to test serialization of exact bytes
// already present on the node, including csh backslash/newline combinations.
func TestDebugSnapshotQuoting(t *testing.T) {
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		t.Run(shell, func(t *testing.T) {
			shellPath, err := exec.LookPath(shell)
			if err != nil {
				t.Skip(err)
			}
			dir := filepath.Join(t.TempDir(), "snapshot space ' ! \\ dir")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			helper, err := createDebugSnapshot(dir, shellPath, &engine.Step{})
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{
				"VALUE0": "", "VALUE1": "\\\n", "VALUE2": "\\\\\n", "VALUE3": "'\\\n!",
				"VALUE4": "!foo \" $HOME `touch forbidden` $(touch forbidden)",
				"VALUE5": "中文\t日文\n\n", "VALUE6": "carriage\rreturn", "VALUE7": "\\'\\\"\\!",
			}
			capture := exec.Command("/bin/sh", helper)
			capture.Dir = dir
			capture.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
			for k, v := range want {
				capture.Env = append(capture.Env, k+"="+v)
			}
			if out, err := capture.CombinedOutput(); err != nil {
				t.Fatalf("capture: %v %s", err, out)
			}
			load := ". "
			args := []string{"-c"}
			if shell != "sh" {
				load = "source "
			}
			if isCShell(shell) {
				args = []string{"-f", "-c"}
			}
			args = append(args, load+debugSourceQuote(filepath.Join(dir, debugEnvFilename(shell)), shell)+"\n/usr/bin/env -0\n")
			restore := exec.Command(shellPath, args...)
			restore.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
			restore.Dir = dir
			out, err := restore.CombinedOutput()
			if err != nil {
				t.Fatalf("source: %v %s", err, out)
			}
			got := map[string]string{}
			for _, item := range strings.Split(string(out), "\x00") {
				if k, v, ok := strings.Cut(item, "="); ok {
					got[k] = v
				}
			}
			for k, v := range want {
				if actual, ok := got[k]; !ok || actual != v {
					t.Errorf("%s: got %q want %q", k, actual, v)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
				t.Fatal("snapshot executed environment value")
			}
		})
	}
}

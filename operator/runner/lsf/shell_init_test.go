package lsf

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
)

func TestShellStartupEnvironment(t *testing.T) {
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		for _, mode := range []string{"default", "disabled", "runner-disabled", "step-enabled", "clone", "clone-disabled", "missing-rc"} {
			t.Run(shell+"/"+mode, func(t *testing.T) {
				csh := shell == "tcsh" || shell == "csh"
				home := t.TempDir()
				overrideHome := t.TempDir()
				clone := strings.HasPrefix(mode, "clone")
				enabled := mode != "disabled" && mode != "runner-disabled" && mode != "clone-disabled"
				loaded := enabled && mode != "missing-rc"
				rcName, rc := ".profile", "export RC_ENV=from-rc\nexport ENV=from-rc\nexport OVERLAY=from-rc\nexport PATH=/rc/bin:/usr/bin:/bin\nexport LSB_JOBID=spoofed\nrc_local=local-ok\nrc_alias() { echo alias-ok; }\ncd /\nset -x\n"
				if shell == "bash" {
					rcName = ".bashrc"
				}
				if csh {
					rcName = ".cshrc"
					rc = "setenv RC_ENV from-rc\nsetenv ENV from-rc\nsetenv OVERLAY from-rc\nsetenv PATH /rc/bin:/usr/bin:/bin\nsetenv LSB_JOBID spoofed\nset rc_local = local-ok\nalias rc_alias 'echo alias-ok'\ncd /\nset echo\n"
				}
				if mode != "missing-rc" {
					writeStartupFile(t, filepath.Join(home, rcName), rc)
				}
				// Explicit HOME applies after startup, never chooses the rc account.
				writeStartupFile(t, filepath.Join(overrideHome, rcName), "exit 91\n")
				injected := filepath.Join(home, "must-not-execute")
				secret := "secret ' \" $literal ! `touch " + injected + "` $(touch " + injected + ")\nsecond line\n\n"
				env := map[string]string{"HOME": overrideHome, "OVERLAY": "from-step", "PATH": "/step/bin:/usr/bin:/bin", "LSB_JOBID": "yaml-spoof"}
				if mode == "disabled" || mode == "clone-disabled" {
					env["SHELL_INIT"] = "false"
				}
				if mode == "step-enabled" {
					env["SHELL_INIT"] = "true"
				}
				outputEnv := filepath.Join(home, "environment")
				outputCWD := filepath.Join(home, "cwd")
				commands := "/usr/bin/env -0 > " + quote(outputEnv) + "\npwd > " + quote(outputCWD) + "\n"
				if loaded && !clone {
					commands += "echo \"$rc_local\"\nrc_alias\n"
				}
				log, workspace, err := runStartupStep(t, shell, home, commands, env, clone, mode == "runner-disabled" || mode == "step-enabled", secret)
				if err != nil {
					t.Fatalf("startup: %v\n%s", err, log)
				}
				data, err := os.ReadFile(outputEnv)
				if err != nil {
					t.Fatal(err)
				}
				got := map[string]string{}
				for _, item := range strings.Split(string(data), "\x00") {
					if key, value, ok := strings.Cut(item, "="); ok {
						got[key] = value
					}
				}
				want := map[string]string{"HOME": overrideHome, "OVERLAY": "from-step", "PATH": "/step/bin:/usr/bin:/bin", "LSB_JOBID": "321", "FROM_SECRET": secret, "LSF_ENVDIR": "/node/lsf", "BASH_ENV": filepath.Join(home, "implicit.sh")}
				if loaded {
					want["RC_ENV"] = "from-rc"
					want["ENV"] = "from-rc"
				} else {
					want["RC_ENV"] = ""
					want["ENV"] = ""
				}
				for key, value := range want {
					if got[key] != value {
						t.Errorf("environment %s mismatch", key)
					}
				}
				for key := range got {
					if strings.HasPrefix(key, shellEnvPrefix) || key == "DRONE_LSF_CLONE_NETRC_HOME" {
						t.Errorf("internal environment leaked: %s", key)
					}
				}
				if data, err := os.ReadFile(outputCWD); err != nil || strings.TrimSpace(string(data)) != workspace {
					t.Fatalf("startup did not restore workspace: %v %s", err, data)
				}
				if loaded && !clone && (!strings.Contains(log, "local-ok") || !strings.Contains(log, "alias-ok")) {
					t.Fatalf("shell state lost: %s", log)
				}
				if strings.Contains(log, "secret '") {
					t.Fatal("startup tracing exposed a secret")
				}
				if _, err := os.Stat(injected); !os.IsNotExist(err) {
					t.Fatal("environment value was executed as shell source")
				}
			})
		}
	}
}

func TestTcshStartupFilePrecedence(t *testing.T) {
	home := t.TempDir()
	writeStartupFile(t, filepath.Join(home, ".cshrc"), "exit 99\n")
	writeStartupFile(t, filepath.Join(home, ".tcshrc"), "set rc_local = preferred\necho startup-once\n")
	log, _, err := runStartupStep(t, "tcsh", home, "echo $rc_local\n", nil, false, false, "")
	if err != nil || strings.Count(log, "startup-once") != 1 || !strings.Contains(log, "preferred") {
		t.Fatalf("tcsh rc selection: %v %s", err, log)
	}
}

func TestShellStartupFailure(t *testing.T) {
	for _, shell := range []string{"tcsh", "bash", "sh"} {
		for _, rc := range []string{"exec /bin/sh -c 'exit 0'\n", "exec /bin/sh -c 'exit 7'\n"} {
			t.Run(shell+"/"+strings.TrimSpace(rc), func(t *testing.T) {
				home := t.TempDir()
				name := map[string]string{"tcsh": ".cshrc", "bash": ".bashrc", "sh": ".profile"}[shell]
				writeStartupFile(t, filepath.Join(home, name), rc)
				log, _, err := runStartupStep(t, shell, home, "echo commands-must-not-run\n", nil, false, false, "")
				if err == nil || strings.Contains(log, "\ncommands-must-not-run\n") || strings.Contains(log, "\nmust-not-run\n") || !strings.Contains(log, "initialization did not complete") {
					t.Fatalf("startup failure: %v %s", err, log)
				}
			})
		}
	}
}

func TestShellStartupNonzeroProbes(t *testing.T) {
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		t.Run(shell, func(t *testing.T) {
			home := t.TempDir()
			name := map[string]string{"tcsh": ".cshrc", "csh": ".cshrc", "bash": ".bashrc", "sh": ".profile"}[shell]
			assignment, check := "export AFTER_PROBE=loaded", "test \"$AFTER_PROBE\" = loaded"
			if isCShell(shell) {
				assignment, check = "setenv AFTER_PROBE loaded", "if (\"$AFTER_PROBE\" != loaded) exit 9"
			}
			// Reproduce production: normal startup succeeds, -e exits silently
			// with status 2. A final nonzero status must not reject initialization.
			writeStartupFile(t, filepath.Join(home, name), "/bin/sh -c 'exit 2'\n"+assignment+"\n/bin/sh -c 'exit 2'\n")
			log, _, err := runStartupStep(t, shell, home, check+"\necho commands-ran\n", nil, false, false, "")
			if err != nil || !strings.Contains(log, "commands-ran") || !strings.Contains(log, "Startup returned status 2; continuing") {
				t.Fatalf("nonzero startup probe: %v %s", err, log)
			}
		})
	}
}

func TestInitializedCommandsFailFast(t *testing.T) {
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		for _, failure := range []string{"/bin/sh -c 'exit 7'", "echo block-start\n/bin/sh -c 'exit 7'"} {
			t.Run(shell+"/"+failure, func(t *testing.T) {
				home := t.TempDir()
				assignment := "local_value=preserved"
				if isCShell(shell) {
					assignment = "set local_value = preserved"
				}
				commands := []string{assignment, "echo $local_value", failure, "echo must-not-run"}
				log, _, err := runStartupStep(t, shell, home, "", nil, false, false, "", commands)
				status, ok := err.(*exec.ExitError)
				if !ok || status.ExitCode() != 7 || strings.Contains(log, "must-not-run") || !strings.Contains(log, "preserved") {
					t.Fatalf("command failure lost: %v %s", err, log)
				}
			})
		}
	}
}

func writeStartupFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

// Exercise the actual generated wrapper on execution-node values without LSF.
func runStartupStep(t *testing.T, shell, home, commands string, env map[string]string, clone, disable bool, secret string, yamlCommands ...[]string) (string, string, error) {
	t.Helper()
	path, err := exec.LookPath(shell)
	if err != nil {
		t.Skip(err)
	}
	e, err := New(Config{Bsub: "/bin/true", Bjobs: "/bin/true", Bkill: "/bin/true", Shell: path, DisableShellInit: disable, Workspace: filepath.Join(t.TempDir(), "work space")})
	if err != nil {
		t.Fatal(err)
	}
	items := []string{"echo placeholder"}
	if len(yamlCommands) != 0 {
		items = yamlCommands[0]
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	spec := testSpec(t, "kind: pipeline\ntype: lsf\nclone: {disable: true}\nsteps:\n- name: test\n  commands: "+string(encoded)+"\n")
	step := spec.Steps[0]
	for key, value := range env {
		step.Envs[key] = value
	}
	if secret != "" {
		step.Secrets = append(step.Secrets, &engine.SecretVar{Name: "test-secret", Env: "FROM_SECRET"})
		spec.Secrets = append(spec.Secrets, &engine.Secret{Metadata: engine.Metadata{Name: "test-secret"}, Data: secret})
	}
	if len(yamlCommands) == 0 {
		spec.Files[0].Data = []byte(commands)
		delete(spec.Files[0].Metadata.Labels, commandsLabel)
	}
	if clone {
		spec.Files[0].Metadata.Labels["lsf.drone.io/clone"] = "true"
	}
	ctx := context.Background()
	if err := e.Setup(ctx, spec); err != nil {
		t.Fatal(err)
	}
	defer e.Destroy(ctx, spec)
	if err := e.Create(ctx, spec, step); err != nil {
		t.Fatal(err)
	}
	p, _ := e.lookup(spec)
	j := p.jobs[step]
	writeStartupFile(t, filepath.Join(home, "implicit.sh"), "exit 95\n")
	cmd := exec.Command("/bin/sh", j.wrapper)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "LSB_JOBID=321", "LSF_ENVDIR=/node/lsf", "DRONE_LSF_CLONE_NETRC_HOME=/stale/parent", "BASH_ENV=" + filepath.Join(home, "implicit.sh")}
	out, runErr := cmd.CombinedOutput()
	log, err := os.ReadFile(j.log)
	if err != nil {
		t.Fatal(err)
	}
	return string(out) + string(log), p.workspace, runErr
}

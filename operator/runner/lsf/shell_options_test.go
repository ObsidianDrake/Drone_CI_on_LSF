package lsf

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-yaml/yaml"
)

func TestShellOptionValidation(t *testing.T) {
	for _, test := range []struct {
		shell, options string
		init, valid    bool
	}{
		{"tcsh", "-f", true, false},
		{"csh", "-ef", true, false},
		{"/bin/tcsh", "'-f' -e", false, true},
		{"tcsh", "-xv", true, true},
		{"bash", "-f", true, true},
		{"sh", "-fu", true, true},
		{"bash", "--norc", true, false},
		{"bash", "--noprofile", true, true},
		{"bash", "--norc --noprofile", false, true},
		{"bash", "-eu -o 'pipefail' -O nullglob", true, true},
		{"sh", "-o nounset +o noglob", false, true},
		{"sh", "-o pipefail", true, false},
		{"bash", "-o", true, false},
		{"bash", "-O", true, false},
		{"bash", "-o 'pipefail", true, false},
		{"bash", "-o $(touch /tmp/no)", true, false},
		{"bash", "+e", true, false},
		{"bash", "+o errexit", true, false},
		{"bash", "-O extdebug", true, false},
		{"bash", "--rcfile /tmp/rc", false, false},
		{"bash", "--init-file=/tmp/rc", true, false},
		{"bash", "--login", false, false},
		{"bash", "--posix", false, false},
		{"tcsh", "-m", false, false},
		{"tcsh", "-d", false, false},
	} {
		t.Run(test.shell+"/"+test.options, func(t *testing.T) {
			_, err := parseShellOptions(test.shell, &test.init, test.options)
			if (err == nil) != test.valid {
				t.Fatalf("init=%v valid=%v: %v", test.init, test.valid, err)
			}
		})
	}
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		for _, options := range []string{"-c 'echo bypass'", "-ec", "-i", "-l", "-s", "-n", "-t", "--", "-", "script.sh", "''", "--help", "--version", "-f\x00"} {
			if _, err := parseShellOptions(shell, nil, options); err == nil {
				t.Errorf("accepted %s %q", shell, options)
			}
		}
	}
}

func TestBashOptionOrdering(t *testing.T) {
	init := false
	options, err := parseShellOptions("bash", &init, "-u --norc -o pipefail --noprofile")
	if err != nil {
		t.Fatal(err)
	}
	got, err := shellCommand("bash", init, options)
	want := []string{"bash", "--noprofile", "--norc", "--norc", "--noprofile", "-u", "-o", "pipefail", "-e"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q: %v", got, err)
	}
}

func TestShellOptionsYAMLLint(t *testing.T) {
	for _, test := range []struct {
		name, step string
		valid      bool
	}{
		{"inherited conflict", "", false},
		{"step disables init", "  environment: {SHELL_INIT: 'false'}\n", true},
		{"step changes shell", "  environment: {SHELL_TYPE: bash}\n", true},
		{"step clears options", "  environment: {SHELL_OPTION: ''}\n", true},
		{"bash conflict", "  environment: {SHELL_TYPE: bash, SHELL_OPTION: --norc}\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := "kind: pipeline\ntype: lsf\nclone: {disable: true}\nenvironment:\n  SHELL_TYPE: tcsh\n  SHELL_INIT: 'true'\n  SHELL_OPTION: -f\nsteps:\n- name: build\n" + test.step + "  commands: [echo ok]\n"
			manifest, err := yaml.ParseString(raw)
			if err != nil {
				t.Fatal(err)
			}
			pipeline := manifest.Resources[0].(*yaml.Pipeline)
			if err := ApplyEnvironment(pipeline, raw); err != nil {
				t.Fatal(err)
			}
			err = Lint(pipeline, true)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if err != nil && (!strings.Contains(err.Error(), `step "build"`) || !strings.Contains(err.Error(), "SHELL_INIT=true")) {
				t.Fatalf("missing actionable error: %v", err)
			}
		})
	}
}

func TestResolvedShellOptionsPreflight(t *testing.T) {
	for _, test := range []struct {
		name    string
		disable bool
		secret  bool
		valid   bool
	}{
		{"default init conflicts", false, false, false},
		{"disabled default allows fast shell", true, false, true},
		{"secret option conflicts", false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := testEngine(t)
			e.config.DisableShellInit = test.disable
			spec := testSpec(t, "kind: pipeline\ntype: lsf\nsteps:\n- name: build\n  commands: [echo ok]\n")
			// Includes an automatic clone before build. Invalid options in build
			// must be found before Setup creates a workspace or submits clone.
			step := spec.Steps[len(spec.Steps)-1]
			if test.secret {
				step.Secrets = append(step.Secrets, &engine.SecretVar{Name: "options", Env: "SHELL_OPTION"})
				spec.Secrets = append(spec.Secrets, &engine.Secret{Metadata: engine.Metadata{Name: "options"}, Data: "-f"})
			} else {
				step.Envs["SHELL_OPTION"] = "-f"
			}
			err := e.Setup(context.Background(), spec)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if err == nil {
				e.Destroy(context.Background(), spec)
			} else {
				if !strings.Contains(err.Error(), `step "build"`) || !strings.Contains(err.Error(), "SHELL_INIT=true") {
					t.Fatal(err)
				}
				if _, statErr := os.Stat(e.config.Workspace); !os.IsNotExist(statErr) {
					t.Fatalf("invalid pipeline created workspace: %v", statErr)
				}
			}
		})
	}
}

func TestShellOptionsExecution(t *testing.T) {
	for _, shell := range []string{"bash", "sh"} {
		t.Run(shell+"/noglob", func(t *testing.T) {
			home := t.TempDir()
			file := ".profile"
			if shell == "bash" {
				file = ".bashrc"
			}
			writeStartupFile(t, filepath.Join(home, file), "export RC_ENV=loaded\n")
			log, _, err := runStartupStep(t, shell, home, "test \"$RC_ENV\" = loaded\ntouch match.txt\nset -- *.txt\ntest \"$1\" = '*.txt'\necho options-ok\n", map[string]string{"SHELL_OPTION": "-fu"}, false, false, "")
			if err != nil || !strings.Contains(log, "options-ok") {
				t.Fatalf("options did not reach shell: %v\n%s", err, log)
			}
		})
	}
	t.Run("bash/pipefail", func(t *testing.T) {
		log, _, err := runStartupStep(t, "bash", t.TempDir(), "false | true\necho should-not-run\n", map[string]string{"SHELL_OPTION": "-o 'pipefail'"}, false, false, "")
		if err == nil || strings.Contains(log, "should-not-run") {
			t.Fatalf("pipefail ignored: %v\n%s", err, log)
		}
	})
	for _, shell := range []string{"tcsh", "csh", "bash", "sh"} {
		t.Run(shell+"/trace", func(t *testing.T) {
			log, _, err := runStartupStep(t, shell, t.TempDir(), "echo trace-ok\n", map[string]string{"SHELL_OPTION": "-x"}, false, false, "private-secret-bytes")
			if err != nil || !strings.Contains(log, "echo trace-ok") || strings.Contains(log, "private-secret-bytes") {
				t.Fatalf("tracing failed or exposed bootstrap secret: %v\n%s", err, log)
			}
		})
	}
	for _, shell := range []string{"tcsh", "csh"} {
		t.Run(shell+"/fast", func(t *testing.T) {
			home := t.TempDir()
			writeStartupFile(t, filepath.Join(home, ".cshrc"), "echo rc-must-not-run\n")
			log, _, err := runStartupStep(t, shell, home, "echo fast-ok\n", map[string]string{"SHELL_INIT": "false", "SHELL_OPTION": "-f"}, false, false, "")
			if err != nil || !strings.Contains(log, "fast-ok") || strings.Contains(log, "rc-must-not-run") {
				t.Fatalf("fast shell: %v\n%s", err, log)
			}
		})
	}
}

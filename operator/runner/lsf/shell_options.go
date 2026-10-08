package lsf

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/drone/drone-runtime/engine"
)

type shellOptions struct {
	args           []string
	trace, verbose bool
}

// A nil initialize defers conflict checks until runner defaults and secrets are
// resolved. An empty shell similarly defers shell-specific option checks.
func parseShellOptions(shell string, initialize *bool, input string) (shellOptions, error) {
	var out shellOptions
	args, err := splitArguments(input, "SHELL_OPTION")
	if err != nil {
		return out, err
	}
	if shell == "" {
		return out, nil
	}
	base := filepath.Base(shell)
	csh := isCShell(shell)
	var longs, shorts []string
	unsupported := func() (shellOptions, error) {
		// Do not echo option text: it may have come from a secret.
		return out, fmt.Errorf("lsf: SHELL_OPTION contains an unsupported option for %s; command, script, interactive, login and startup-file selection are managed by Drone", base)
	}
	conflict := func(option string) (shellOptions, error) {
		return out, fmt.Errorf("lsf: SHELL_OPTION %s conflicts with SHELL_INIT=true for %s; remove the option or set SHELL_INIT=false", option, base)
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if base == "bash" && (arg == "--norc" || arg == "--noprofile") {
			if arg == "--norc" && initialize != nil && *initialize {
				return conflict(arg)
			}
			longs = append(longs, arg)
			continue
		}
		if !csh && (arg == "-o" || arg == "+o" || (base == "bash" && (arg == "-O" || arg == "+O"))) {
			i++
			if i == len(args) {
				return out, fmt.Errorf("lsf: SHELL_OPTION %s requires an option name", arg)
			}
			name := args[i]
			enable := arg[0] == '-'
			if arg[1] == 'O' {
				switch name {
				case "nullglob", "failglob", "dotglob", "nocaseglob", "extglob", "globstar", "expand_aliases":
				default:
					return unsupported()
				}
			} else {
				switch name {
				case "errexit":
					if !enable {
						return out, fmt.Errorf("lsf: SHELL_OPTION cannot disable Drone's errexit handling")
					}
				case "allexport", "noclobber", "noglob", "nounset":
				case "pipefail", "errtrace", "functrace", "braceexpand", "hashall", "histexpand", "physical":
					if base != "bash" {
						return unsupported()
					}
				case "xtrace":
					out.trace = enable
				case "verbose":
					out.verbose = enable
				default:
					return unsupported()
				}
			}
			shorts = append(shorts, arg, name)
			continue
		}
		if len(arg) < 2 || (arg[0] != '-' && (csh || arg[0] != '+')) {
			return unsupported()
		}
		allowed := "aefuCvx"
		if csh {
			allowed = "efvxVX"
		} else if base == "bash" {
			allowed += "hBEHPT"
		}
		for _, flag := range arg[1:] {
			if !strings.ContainsRune(allowed, flag) {
				return unsupported()
			}
			if csh && flag == 'f' && initialize != nil && *initialize {
				return conflict("-f")
			}
			if flag == 'e' && arg[0] == '+' {
				return out, fmt.Errorf("lsf: SHELL_OPTION cannot disable Drone's errexit handling")
			}
			switch flag {
			case 'x', 'X':
				out.trace = arg[0] == '-'
			case 'v', 'V':
				out.verbose = arg[0] == '-'
			}
		}
		shorts = append(shorts, arg)
	}
	// Bash requires long options before short options, including runner flags.
	out.args = append(longs, shorts...)
	return out, nil
}

func shellCommand(shell string, initialize bool, options shellOptions) ([]string, error) {
	args, err := shellStartupArgs(shell, initialize)
	if err != nil {
		return nil, err
	}
	if filepath.Base(shell) == "bash" {
		// Keep long options before -e and any user short options.
		args = []string{shell, "--noprofile", "--norc"}
		return append(append(args, options.args...), "-e"), nil
	}
	return append(args, options.args...), nil
}

func (e *Engine) shellSettings(env map[string]string) (string, bool, shellOptions, error) {
	shell := env["SHELL_TYPE"]
	if shell == "" {
		shell = e.config.Shell
	}
	initialize, err := shellInitEnabled(env["SHELL_INIT"], !e.config.DisableShellInit)
	if err != nil {
		return shell, false, shellOptions{}, err
	}
	if _, err := shellArgs(shell); err != nil {
		return shell, initialize, shellOptions{}, err
	}
	options, err := parseShellOptions(shell, &initialize, env["SHELL_OPTION"])
	return shell, initialize, options, err
}

// stepEnvironment also resolves secret-backed shell controls before validation.
func stepEnvironment(spec *engine.Spec, step *engine.Step) (map[string]string, error) {
	env := make(map[string]string, len(step.Envs))
	for key, value := range step.Envs {
		env[key] = value
	}
	for _, ref := range step.Secrets {
		found := false
		for _, secret := range spec.Secrets {
			if secret.Metadata.Name == ref.Name {
				env[ref.Env] = secret.Data
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("lsf: missing secret %q", ref.Name)
		}
	}
	return env, nil
}

// Validate checks every runnable step before any job (including clone) can be
// submitted, using the same effective environment and defaults as Create.
func (e *Engine) Validate(spec *engine.Spec) error {
	for _, step := range spec.Steps {
		if step.RunPolicy == engine.RunNever {
			continue
		}
		env, err := stepEnvironment(spec, step)
		if err == nil {
			_, _, _, err = e.shellSettings(env)
		}
		if err != nil {
			return fmt.Errorf("lsf: step %q: %w", step.Metadata.Name, err)
		}
	}
	return nil
}

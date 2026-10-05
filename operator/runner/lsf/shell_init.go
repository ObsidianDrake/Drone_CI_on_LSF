package lsf

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// Values travel as environment entries, never as shell source. This preserves
// quotes, substitutions, newlines and secret bytes in both shell families.
const shellEnvPrefix = "__DRONE_LSF_"

func shellEnvName(i int) string { return fmt.Sprintf("%sENV_%d", shellEnvPrefix, i) }

func isCShell(shell string) bool {
	return filepath.Base(shell) == "tcsh" || filepath.Base(shell) == "csh"
}

func shellInitEnabled(value string, defaultValue bool) (bool, error) {
	if value == "" {
		return defaultValue, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("lsf: SHELL_INIT must be true or false")
	}
	return enabled, nil
}

func shellStartupArgs(shell string, initialize bool) ([]string, error) {
	args, err := shellArgs(shell)
	if err == nil && initialize && isCShell(shell) {
		// Let the installed shell select system and personal rc files using its
		// native rules (.tcshrc before .cshrc for tcsh), exactly once.
		// Personal rc files commonly contain probes with nonzero status. tcsh
		// cannot toggle -e in-place, so commands get explicit boundary checks.
		args = []string{shell}
	}
	return args, err
}

func shellStartupScript(shell string, initialize, clone bool, keys []string, commands []byte) string {
	csh := isCShell(shell)
	var s strings.Builder
	if !csh {
		s.WriteString("set +x\nset +v\n")
		fmt.Fprintf(&s, "export BASH_ENV=\"${%sBASH_ENV}\" ENV=\"${%sENV}\"\nunset %sBASH_ENV %sENV\n", shellEnvPrefix, shellEnvPrefix, shellEnvPrefix, shellEnvPrefix)
	}
	if initialize && !csh {
		s.WriteString("set +e\n")
		file := ".profile"
		if filepath.Base(shell) == "bash" {
			file = ".bashrc"
		}
		fmt.Fprintf(&s, "if [ -n \"${HOME:-}\" ] && [ -f \"$HOME/%s\" ]; then\n  printf '%%s\\n' \"[environment] Loading $HOME/%s\"\n  . \"$HOME/%s\"\n  __drone_lsf_rc_status=$?\n  if [ \"$__drone_lsf_rc_status\" -ne 0 ]; then printf '[environment] Startup returned status %%s; continuing\\n' \"$__drone_lsf_rc_status\"; fi\n  unset __drone_lsf_rc_status\nfi\n", file, file, file)
	}
	if csh {
		// Rc files sometimes enable tracing. Turn it off before copying secrets.
		s.WriteString("set __drone_lsf_rc_status = $status\nunset echo verbose\n")
		s.WriteString("if ($__drone_lsf_rc_status != 0) /usr/bin/printf '[environment] Startup returned status %s; continuing\\n' \"$__drone_lsf_rc_status\"\nunset __drone_lsf_rc_status\nunsetenv DRONE_LSF_CLONE_NETRC_HOME\n")
	} else {
		s.WriteString("set +x\nset +v\nset -e\nunset DRONE_LSF_CLONE_NETRC_HOME\n")
	}
	for i, key := range keys {
		name := shellEnvName(i)
		if csh {
			fmt.Fprintf(&s, "setenv %s \"${%s:q}\"\nif ($status != 0) exit $status\nunsetenv %s\n", key, name, name)
		} else {
			fmt.Fprintf(&s, "export %s=\"${%s}\"\nunset %s\n", key, name, name)
		}
	}
	// Startup files may cd elsewhere; commands always begin in their workspace.
	ref := func(name string) string {
		if csh {
			return "\"${" + shellEnvPrefix + name + ":q}\""
		}
		return "\"${" + shellEnvPrefix + name + "}\""
	}
	s.WriteString("cd " + ref("WORKDIR") + "\n")
	if csh {
		s.WriteString("if ($status != 0) exit $status\n")
	}
	s.WriteString("/usr/bin/printf '' > " + ref("READY") + "\n")
	if csh {
		s.WriteString("if ($status != 0) exit $status\n")
	}
	if csh {
		s.WriteString("unsetenv " + shellEnvPrefix + "WORKDIR\nunsetenv " + shellEnvPrefix + "READY\n")
	} else {
		s.WriteString("unset " + shellEnvPrefix + "WORKDIR " + shellEnvPrefix + "READY\n")
	}
	if clone {
		// Only exported state crosses into clone's POSIX shell. The selected
		// shell's aliases and local variables remain available in ordinary steps.
		s.WriteString("exec /usr/bin/env -u " + shellEnvPrefix + "COMMANDS /bin/sh -e " + ref("COMMANDS") + "\n")
	} else {
		s.Write(commands)
	}
	return s.String()
}

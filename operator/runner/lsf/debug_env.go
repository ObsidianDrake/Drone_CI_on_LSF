package lsf

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drone/drone-runtime/engine"
)

//go:embed debug_env.awk
var debugEnvAWK string

func debugEnvFilename(shell string) string {
	if isCShell(shell) {
		return "env.csh"
	}
	if filepath.Base(shell) == "bash" {
		return "env.bash"
	}
	return "env.sh"
}

// Runs on the execution node; no server binary or Python is required there.
// Only exclusion names are written here, never secret values.
func createDebugSnapshot(dir, shell string, step *engine.Step) (string, error) {
	secrets := []string{"CI_NETRC_USERNAME", "CI_NETRC_PASSWORD", "DRONE_NETRC_USERNAME", "DRONE_NETRC_PASSWORD"}
	for _, secret := range step.Secrets {
		if envPattern.MatchString(secret.Env) {
			secrets = append(secrets, secret.Env)
		}
	}
	sort.Strings(secrets)
	awkPath := filepath.Join(dir, "snapshot.awk")
	if err := os.WriteFile(awkPath, []byte(debugEnvAWK), 0600); err != nil {
		return "", err
	}
	output := filepath.Join(dir, debugEnvFilename(shell))
	load := "source "
	if filepath.Base(shell) == "sh" {
		load = ". "
	}
	script := "#!/bin/sh\nset +x\nset +v\numask 077\n" +
		"export __DRONE_LSF_SNAPSHOT_SHELL=" + quote(shell) + "\n" +
		"export __DRONE_LSF_SNAPSHOT_SECRETS=" + quote(strings.Join(secrets, " ")) + "\n" +
		"__DRONE_LSF_SNAPSHOT_CWD=$(pwd -P)\nexport __DRONE_LSF_SNAPSHOT_CWD\n" +
		"__DRONE_LSF_SNAPSHOT_HOST=$(/bin/hostname)\nexport __DRONE_LSF_SNAPSHOT_HOST\n" +
		"snapshot_tmp=" + quote(output+".tmp") + "\n" +
		"trap '/bin/rm -f -- \"$snapshot_tmp\"' 0\n" +
		"if /usr/bin/awk -f " + quote(awkPath) + " > \"$snapshot_tmp\" && /bin/chmod 600 \"$snapshot_tmp\" && /bin/mv -f -- \"$snapshot_tmp\" " + quote(output) + "; then\n" +
		"  printf '%s\\n' " + quote("[Debug] Step environment saved: "+output) + "\n" +
		"  printf '%s\\n' " + quote(fmt.Sprintf("[Debug] Restore in %s: %s%s", filepath.Base(shell), load, debugSourceQuote(output, shell))) + "\n" +
		"  printf '%s\\n' '[Debug] Managed secrets, clone credentials and original LSF job identity are excluded.'\n" +
		"else\n  printf '%s\\n' '[Debug] WARNING: environment snapshot could not be saved; commands will continue.'\nfi\nexit 0\n"
	path := filepath.Join(dir, "snapshot.sh")
	return path, os.WriteFile(path, []byte(script), 0600)
}

func debugSourceQuote(value, shell string) string {
	if !isCShell(shell) {
		return quote(value)
	}
	var out strings.Builder
	out.WriteByte('\'')
	for _, c := range value {
		switch c {
		case '\'':
			out.WriteString("'\\''")
		case '\\':
			out.WriteString("'\\\\'")
		case '!':
			out.WriteString("\\!")
		case '\n':
			out.WriteString("\\\n")
		default:
			out.WriteRune(c)
		}
	}
	out.WriteByte('\'')
	return out.String()
}

package lsf

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// splitOptions splits quoted CLI arguments without expanding variables,
// substitutions, globs, or executing a shell.
func splitOptions(input string) ([]string, error) {
	var args []string
	var word strings.Builder
	var quote rune
	escape, started := false, false
	for _, char := range input {
		if escape {
			word.WriteRune(char)
			escape = false
			started = true
			continue
		}
		if char == '\\' && quote != '\'' {
			escape = true
			started = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				word.WriteRune(char)
			}
			continue
		}
		switch {
		case char == '\'' || char == '"':
			quote = char
			started = true
		case unicode.IsSpace(char):
			if started {
				args = append(args, word.String())
				word.Reset()
				started = false
			}
		default:
			word.WriteRune(char)
			started = true
		}
	}
	if escape || quote != 0 {
		return nil, fmt.Errorf("lsf: BSUB_OPTION has an unfinished quote or escape")
	}
	if started {
		args = append(args, word.String())
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return nil, fmt.Errorf("lsf: BSUB_OPTION must contain bsub options, not a command")
	}
	// These modes replace the managed command, hide the job ID, or block bsub.
	for _, arg := range args {
		flag := strings.SplitN(arg, "=", 2)[0]
		switch flag {
		case "--", "-I", "-Ip", "-Is", "-IS", "-ISp", "-ISs", "-K", "-J", "-cwd", "-o", "-oo", "-e", "-eo", "-env", "-i", "-is", "-Zs", "-h", "-V", "-pack":
			return nil, fmt.Errorf("lsf: bsub option %s is managed by Drone or incompatible with asynchronous steps", flag)
		}
	}
	return args, nil
}

func shellArgs(shell string) ([]string, error) {
	if strings.ContainsAny(shell, " \t\r\n\x00") {
		return nil, fmt.Errorf("lsf: SHELL_TYPE must name a shell without arguments")
	}
	if !filepath.IsAbs(shell) && strings.Contains(shell, "/") {
		return nil, fmt.Errorf("lsf: shell path must be absolute")
	}
	switch filepath.Base(shell) {
	case "csh", "tcsh":
		return []string{shell, "-f", "-e"}, nil
	case "sh":
		return []string{shell, "-e"}, nil
	case "bash":
		return []string{shell, "--noprofile", "--norc", "-e"}, nil
	default:
		return nil, fmt.Errorf("lsf: unsupported shell %q; use csh, tcsh, sh or bash", shell)
	}
}

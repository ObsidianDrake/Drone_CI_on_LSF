package lsf

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandScriptColorsAndShellState(t *testing.T) {
	for _, shell := range []string{"csh", "tcsh", "sh", "bash"} {
		t.Run(shell, func(t *testing.T) {
			if _, err := exec.LookPath(shell); err != nil {
				t.Skip(err)
			}
			assignment := "value=success"
			if shell == "csh" || shell == "tcsh" {
				assignment = "set value = success"
			}
			commands := []string{assignment, `echo "$value"`, `echo "中文 100% 'quoted'"`, "false", "echo must-not-run"}
			path := filepath.Join(t.TempDir(), "commands.script")
			if err := os.WriteFile(path, []byte(commandScript(commands)), 0600); err != nil {
				t.Fatal(err)
			}
			args, err := shellArgs(shell)
			if err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(args[0], append(args[1:], path)...).CombinedOutput()
			if err == nil {
				t.Fatal("failure did not stop script")
			}
			want := "\x1b[32m+ " + assignment + "\x1b[0;37m\n" +
				"\x1b[32m+ echo \"$value\"\x1b[0;37m\nsuccess\n" +
				"\x1b[32m+ echo \"中文 100% 'quoted'\"\x1b[0;37m\n中文 100% 'quoted'\n" +
				"\x1b[32m+ false\x1b[0;37m\n"
			if string(output) != want {
				t.Fatalf("unexpected trace/output order: %q", output)
			}
			if strings.Contains(string(output), "must-not-run") {
				t.Fatal("ran command after failure")
			}
		})
	}
}

func TestCloneStatusColors(t *testing.T) {
	lines := []string{"hint: Using 'master' as the name", "From http://example.test/repo", " * branch abc -> FETCH_HEAD", "HEAD is now at abc commit", "fatal: Authentication failed", "error: fetch failed", "warning: message", "unknown diagnostic"}
	for _, nativeClone := range []bool{true, false} {
		cmd := exec.Command("/bin/sh", "-c", stderrReader(nativeClone))
		// Final line intentionally has no newline: it must not be dropped.
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		var want strings.Builder
		for i, line := range lines {
			color := "\x1b[31m"
			if nativeClone && i < 4 {
				color = "\x1b[0;37m"
			}
			want.WriteString(color + line + "\x1b[0;37m\n")
		}
		if string(output) != want.String() {
			t.Fatalf("clone=%v output=%q", nativeClone, output)
		}
	}
}

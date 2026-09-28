package web

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Exercise the shipped component's event handlers with deterministic React hooks
// and API responses, including page boundaries and destructive confirmation.
func TestHistoryUIActions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	script, err := os.ReadFile("history_test.js")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "history-test.js")
	if err := os.WriteFile(path, append(append([]byte{}, historyUI...), script...), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("UI behavior: %v\n%s", err, output)
	}
}

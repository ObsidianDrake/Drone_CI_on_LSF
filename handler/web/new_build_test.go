package web

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNewBuildFeedback(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node required for UI event validation")
	}
	for _, base := range []string{"", "/tools/ci"} {
		t.Run(base, func(t *testing.T) {
			u, err := newUIAssets(base)
			if err != nil {
				t.Fatal(err)
			}
			cached, _ := u.cache.Load(u.main)
			bundle := cached.([]byte)
			file := filepath.Join(t.TempDir(), "main.js")
			if err := os.WriteFile(file, bundle, 0600); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command(node, "--check", file).CombinedOutput(); err != nil {
				t.Fatalf("adapted bundle syntax: %v\n%s", err, out)
			}
			if bytes.Contains(bundle, []byte("New build has started successfully")) {
				t.Fatal("misleading success message remains")
			}
			start := bytes.Index(bundle, []byte("Lb=function(e){"))
			end := bytes.Index(bundle, []byte(",Mb=a(358)"))
			if start < 0 || end <= start {
				t.Fatal("missing adapted repository component")
			}
			parent, _ := json.Marshal(string(bundle[start+3 : end]))
			baseJSON, _ := json.Marshal(base)
			spec, err := os.ReadFile("new_build_test.js")
			if err != nil {
				t.Fatal(err)
			}
			script := string(newBuildUI) + "\nconst parentSource=" + string(parent) + ",base=" + string(baseJSON) + ";\n" + string(spec)
			if out, err := exec.Command(node, "-e", script).CombinedOutput(); err != nil {
				t.Fatalf("New Build UI behavior: %v\n%s", err, out)
			}
		})
	}
}

func TestNewBuildUpgradeFailsClosed(t *testing.T) {
	if _, err := adaptNewBuild([]byte("incompatible bundle")); err == nil {
		t.Fatal("accepted unknown New Build form")
	}
}

package web

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTrendAssetsAndInteractions(t *testing.T) {
	for _, base := range []string{"", "/drone"} {
		assets, err := newUIAssets(base)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := assets.cache.Load(assets.main)
		for _, marker := range []string{`path:"/monitor"`, `title:"Workload trends"`, `function DroneTrends`, `var droneTrendsCSS=`} {
			if !bytes.Contains(data.([]byte), []byte(marker)) {
				t.Fatalf("missing %s", marker)
			}
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	script, err := os.ReadFile("trends_test.js")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "trends.js")
	if err = os.WriteFile(path, append(append([]byte{}, trendsUI...), script...), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(node, path).CombinedOutput(); err != nil {
		t.Fatalf("UI: %v\n%s", err, output)
	}
}

package main

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drone/drone/cmd/drone-server/config"
	"github.com/drone/drone/operator/runner/lsf"
	"github.com/kelseyhightower/envconfig"
)

func TestLSFEngineConfiguration(t *testing.T) {
	if _, err := exec.LookPath("tcsh"); err != nil {
		t.Skip("tcsh required")
	}
	bin, err := filepath.Abs("../../tools/lsf-mock/bin")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRONE_RUNNER_ENGINE", "lsf")
	t.Setenv("DRONE_LSF_BSUB", filepath.Join(bin, "bsub"))
	t.Setenv("DRONE_LSF_BJOBS", filepath.Join(bin, "bjobs"))
	t.Setenv("DRONE_LSF_BKILL", filepath.Join(bin, "bkill"))
	t.Setenv("DRONE_LSF_QUEUE", "test")
	var c config.Config
	if err := envconfig.Process("", &c); err != nil {
		t.Fatal(err)
	}
	if c.LSF.Queue != "test" || c.LSF.Slots != 1 || !c.LSF.ShellInit {
		t.Fatalf("LSF config: %+v", c.LSF)
	}
	backend, err := provideEngine(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := backend.(*lsf.Engine); !ok {
		t.Fatalf("backend: %T", backend)
	}
	c.Runner.Engine = "invalid"
	if _, err := provideEngine(c); err == nil {
		t.Fatal("invalid engine accepted")
	}
	if got := runnerType(config.Config{}); got != "docker" {
		t.Fatalf("default: %s", got)
	}
}

func TestLSFDebugRetentionEnvironment(t *testing.T) {
	t.Setenv("DRONE_RUNNER_ENGINE", "lsf")
	for _, key := range []string{"DRONE_LSF_BSUB", "DRONE_LSF_BJOBS", "DRONE_LSF_BKILL"} {
		t.Setenv(key, "/bin/true")
	}
	t.Setenv("DRONE_LSF_DEBUG_RETENTION", "72h")
	t.Setenv("DRONE_LSF_DEBUG_CLEANUP_INTERVAL", "30m")
	var c config.Config
	if err := envconfig.Process("", &c); err != nil {
		t.Fatal(err)
	}
	if c.LSF.DebugRetention != 72*time.Hour || c.LSF.DebugCleanupInterval != 30*time.Minute {
		t.Fatalf("incorrect Debug durations: %+v", c.LSF)
	}
	if _, err := provideEngine(c); err != nil {
		t.Fatal(err)
	}
	// Engine validation proves both new settings reach the engine factory.
	c.LSF.DebugRetention = -time.Second
	if _, err := provideEngine(c); err == nil {
		t.Fatal("retention not wired")
	}
	c.LSF.DebugRetention = time.Hour
	c.LSF.DebugCleanupInterval = -time.Second
	if _, err := provideEngine(c); err == nil {
		t.Fatal("interval not wired")
	}
}

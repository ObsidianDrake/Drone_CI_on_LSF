package lsf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
	"github.com/drone/drone-yaml/yaml/compiler/transform"
)

func TestCloneAskpass(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth 'quoted")
	password := strings.Repeat("t", 194) + "'\"$` !"
	if err := createCloneAuth(dir, "http://drc:8080/gitea/org/repo.git", "drc", "ci-user", password); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ prompt, want string }{
		{"Username for 'http://drc:8080': ", "ci-user\n"},
		{"Password for 'http://ci-user@drc:8080': ", password + "\n"},
		{"Password for 'http://ci-user@elsewhere:8080': ", ""},
		{"Password for 'https://ci-user@drc:8080': ", ""},
		{"Certificate Password: ", ""},
	} {
		cmd := exec.Command(filepath.Join(dir, "askpass"), tc.prompt)
		out, err := cmd.CombinedOutput()
		if string(out) != tc.want || (err != nil) != (tc.want == "") {
			t.Fatal("unexpected askpass response")
		}
	}
	for _, name := range []string{"username", "password", "askpass", "home"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("credentials accessible by other users")
		}
	}
	data, _ := os.ReadFile(filepath.Join(dir, "askpass"))
	if strings.Contains(string(data), password) {
		t.Fatal("token embedded in script")
	}
}

// Reproduce production with an actual old libcurl parser: the file is readable
// and the host matches, but a 194-byte login is truncated and HTTP returns 401.
func TestOldCurlNetrcLongTokenRegression(t *testing.T) {
	client := os.Getenv("DRONE_TEST_CURL_7_19")
	if client == "" {
		t.Skip("set DRONE_TEST_CURL_7_19 to test libcurl 7.19.7")
	}
	version, err := exec.Command(client, "--version").Output()
	if err != nil || !strings.Contains(string(version), "libcurl/7.19.7") {
		t.Fatal("requires actual libcurl 7.19.7")
	}
	token := strings.Repeat("t", 194)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, _ := r.BasicAuth()
		if user != token || password != "x-oauth-basic" {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(401)
		}
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte("machine 127.0.0.1 login "+token+" password x-oauth-basic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, netrc := range []bool{true, false} {
		args := []string{"-q", "--silent", "--show-error", "--output", "/dev/null", "--write-out", "%{http_code}"}
		want := "401"
		if netrc {
			args = append(args, "--netrc")
		} else {
			args = append(args, "--config", "-")
			want = "200"
		}
		cmd := exec.Command(client, append(args, server.URL)...)
		cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
		if !netrc {
			cmd.Stdin = strings.NewReader("user = \"" + token + ":x-oauth-basic\"\n")
		}
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != want {
			t.Fatalf("netrc=%v: status %s, error %v", netrc, out, err)
		}
	}
}

func TestCloneAuthCleanupInDebug(t *testing.T) {
	for _, startupFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "completed", true: "startup-failed"}[startupFails], func(t *testing.T) {
			e := testEngine(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			if startupFails {
				writeStartupFile(t, filepath.Join(home, ".cshrc"), "exit 7\n")
			}
			spec := testSpec(t, "kind: pipeline\ntype: lsf\nsteps: []\n",
				transform.WithEnviron(map[string]string{"DRONE_REMOTE_URL": "http://drc:8080/gitea/org/repo.git"}),
				transform.WithNetrc("drc", "ci-user", strings.Repeat("t", 194)))
			spec.Metadata.Labels[DebugRetainLabel] = "true"
			ctx := context.Background()
			if err := e.Setup(ctx, spec); err != nil {
				t.Fatal(err)
			}
			defer e.Destroy(ctx, spec)
			step := spec.Steps[0]
			if err := e.Create(ctx, spec, step); err != nil {
				t.Fatal(err)
			}
			p, _ := e.lookup(spec)
			j := p.jobs[step]
			if !startupFails { // Simulate a completed command without a remote network request.
				if err := os.WriteFile(filepath.Join(j.dir, "commands.script"), []byte("exit 0\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("/bin/sh", j.wrapper)
			out, err := cmd.CombinedOutput()
			if (err != nil) != startupFails {
				t.Fatalf("wrapper: %v %s", err, out)
			}
			if _, err := os.Stat(filepath.Join(j.dir, "clone-auth")); !os.IsNotExist(err) {
				t.Fatal("clone credentials retained")
			}
			if err := e.Destroy(ctx, spec); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(p.dir, debugRetentionFile)); err != nil {
				t.Fatal("Debug artifacts not retained")
			}
		})
	}
}

func TestCloneAuthCleanupBeforeStart(t *testing.T) {
	e := retentionEngine(t)
	spec, p := retentionPipeline(t, e, true)
	dir := filepath.Join(p.dir, "step-test")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := createCloneAuth(filepath.Join(dir, "clone-auth"), "http://drc/repo.git", "drc", "ci-user", "token"); err != nil {
		t.Fatal(err)
	}
	p.jobs[&engine.Step{}] = &job{dir: dir}
	if err := e.Destroy(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "clone-auth")); !os.IsNotExist(err) {
		t.Fatal("unsubmitted credentials retained")
	}
}

func TestNativeCloneMinimumGitVersion(t *testing.T) {
	for _, version := range []string{"1.7.1", "1.8.3.1", "2.7.9", "2.8.0.GIT", "2.43.0"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "git")
			script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'git version " + version + "'; exit 0; fi\nexit 99\n"
			if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("/bin/sh", "-c", nativeCloneScript(0))
			cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
			out, _ := cmd.CombinedOutput()
			old := version == "1.7.1" || version == "1.8.3.1" || version == "2.7.9"
			if strings.Contains(string(out), "Git 2.8 or newer is required") != old {
				t.Fatalf("incorrect version gate: %s", out)
			}
		})
	}
}

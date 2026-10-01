package lsf

import (
	"context"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Exercise the generated script over real smart HTTP, not file:// or a fake
// fetch. DRONE_TEST_LEGACY_GIT enables the same cases with Git 1.8.3.1 in CI.
func TestNativeCloneHTTPCompatibility(t *testing.T) {
	modern, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	repo := filepath.Join(root, "repo.git")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(modern, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git(repo, "init", "--bare")
	work := t.TempDir()
	git(work, "init", "-b", "main")
	git(work, "commit", "--allow-empty", "-m", "first")
	first := git(work, "rev-parse", "HEAD")
	git(work, "tag", "-a", "v1", "-m", "annotated tag")
	git(work, "commit", "--allow-empty", "-m", "second")
	tip := git(work, "rev-parse", "HEAD")
	git(work, "push", repo, "main", "refs/tags/v1", "HEAD:refs/pull/7/head")
	backend := filepath.Join(git(work, "--exec-path"), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Fatal(err)
	}
	handler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	server := httptest.NewServer(handler)
	defer server.Close()
	authenticated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "ci-user" || password != "ci-password" {
			w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer authenticated.Close()
	denied := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	defer denied.Close()

	for _, client := range []struct{ name, path string }{{"current", modern}, {"1.8.3.1", os.Getenv("DRONE_TEST_LEGACY_GIT")}} {
		t.Run(client.name, func(t *testing.T) {
			if client.path == "" {
				t.Skip("set DRONE_TEST_LEGACY_GIT to test Git 1.8.3.1")
			}
			clientPath, err := filepath.Abs(client.path)
			if err != nil {
				t.Fatal(err)
			}
			if client.name == "1.8.3.1" {
				out, err := exec.Command(clientPath, "--version").CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != "git version 1.8.3.1" {
					t.Fatalf("expected real Git 1.8.3.1: %v %s", err, out)
				}
			}
			for _, tc := range []struct {
				name, sha, ref   string
				depth            int
				fail, deny, auth bool
			}{
				{name: "branch-tip", sha: tip, ref: "refs/heads/main"},
				{name: "branch-advanced", sha: first, ref: "refs/heads/main"},
				{name: "shallow-tip", sha: tip, ref: "refs/heads/main", depth: 1},
				{name: "shallow-advanced", sha: first, ref: "refs/heads/main", depth: 1},
				{name: "annotated-tag", sha: first, ref: "refs/tags/v1", depth: 1},
				{name: "pull-ref", sha: tip, ref: "refs/pull/7/head", depth: 1},
				{name: "authenticated-shallow-advanced", sha: first, ref: "refs/heads/main", depth: 1, auth: true},
				{name: "unavailable-commit", sha: strings.Repeat("f", 40), ref: "refs/heads/main", depth: 1, fail: true},
				{name: "deleted-ref", sha: strings.Repeat("f", 40), ref: "refs/heads/deleted", fail: true},
				{name: "missing-ref", sha: strings.Repeat("f", 40), fail: true},
				{name: "invalid-ref", sha: strings.Repeat("f", 40), ref: "--upload-pack=unexpected", fail: true},
				{name: "invalid-sha", sha: "--help", ref: "refs/heads/main", fail: true},
				{name: "access-denied", sha: tip, ref: "refs/heads/main", fail: true, deny: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					dir := t.TempDir()
					home := t.TempDir()
					script := filepath.Join(t.TempDir(), "clone.sh")
					if err := os.WriteFile(script, []byte(nativeCloneScript(tc.depth)), 0600); err != nil {
						t.Fatal(err)
					}
					remote := server.URL + "/repo.git"
					if tc.deny {
						remote = denied.URL + "/repo.git"
					}
					if tc.auth {
						remote = authenticated.URL + "/repo.git"
					}
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, "/bin/sh", "-e", script)
					cmd.Dir = dir
					cmd.Env = []string{"HOME=" + home, "PATH=" + filepath.Dir(clientPath) + ":/usr/bin:/bin", "LC_ALL=C",
						"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "DRONE_REMOTE_URL=" + remote,
						"DRONE_COMMIT_SHA=" + tc.sha, "DRONE_COMMIT_REF=" + tc.ref}
					accountNetrc := "machine 127.0.0.1 login personal password do-not-change\n"
					if tc.auth {
						privateHome := t.TempDir()
						for path, data := range map[string]string{
							filepath.Join(home, ".netrc"):        accountNetrc,
							filepath.Join(privateHome, ".netrc"): "machine 127.0.0.1 login ci-user password ci-password\n",
						} {
							if err := os.WriteFile(path, []byte(data), 0600); err != nil {
								t.Fatal(err)
							}
						}
						cmd.Env = append(cmd.Env, "DRONE_LSF_CLONE_NETRC_HOME="+privateHome)
					}
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil || (err != nil) != tc.fail {
						t.Fatalf("unexpected result: %v (context %v)\n%s", err, ctx.Err(), out)
					}
					if tc.fail {
						check := exec.Command(modern, "rev-parse", "--verify", "HEAD")
						check.Dir = dir
						if err := check.Run(); err == nil {
							t.Fatalf("failed clone checked out another commit\n%s", out)
						}
						return
					}
					if head := git(dir, "rev-parse", "HEAD"); head != tc.sha {
						t.Fatalf("HEAD=%s, want %s\n%s", head, tc.sha, out)
					}
					if !strings.Contains(string(out), "[clone] Verified HEAD: "+tc.sha) {
						t.Fatalf("missing commit verification\n%s", out)
					}
					if tc.auth {
						if data, err := os.ReadFile(filepath.Join(home, ".netrc")); err != nil || string(data) != accountNetrc {
							t.Fatalf("account netrc changed: %v", err)
						}
					}
					if client.name == "1.8.3.1" {
						if !strings.Contains(string(out), "no such remote ref "+tc.sha) || !strings.Contains(string(out), "Ref fetch succeeded") {
							t.Fatalf("legacy HTTP clone did not exercise ref fallback\n%s", out)
						}
						if strings.HasSuffix(tc.name, "shallow-advanced") && !strings.Contains(string(out), "Full-history fetch succeeded") {
							t.Fatalf("did not expand shallow history\n%s", out)
						}
					}
				})
			}
		})
	}
}

func TestNativeCloneCompilation(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			config := "kind: pipeline\ntype: lsf\nclone:\n  depth: 1\n"
			if explicit {
				config += "  disable: true\nsteps:\n- name: clone\n  image: git\n"
			} else {
				config += "steps:\n- name: work\n  commands: [echo done]\n"
			}
			spec := testSpec(t, config)
			for _, file := range spec.Files {
				if file.Metadata.Name == "clone" && file.Metadata.Labels["lsf.drone.io/clone"] == "true" && string(file.Data) == nativeCloneScript(1) {
					return
				}
			}
			t.Fatal("native clone did not receive the compatibility script")
		})
	}
}

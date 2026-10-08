package lsf

import (
	"context"
	"fmt"
	"io"
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
// fetch. CI supplies Git 2.8 with libcurl 7.19.7 and Git 2.43 as well.
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
	commit := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, "version.txt"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		git(work, "add", "version.txt")
		git(work, "commit", "-m", content)
	}
	commit("first")
	first := git(work, "rev-parse", "HEAD")
	git(work, "tag", "-a", "v1", "-m", "annotated tag")
	commit("second")
	tip := git(work, "rev-parse", "HEAD")
	git(work, "push", repo, "main", "refs/tags/v1", "HEAD:refs/pull/7/head")
	backend := filepath.Join(git(work, "--exec-path"), "git-http-backend")
	if _, err := os.Stat(backend); err != nil {
		t.Fatal(err)
	}
	handler := &cgi.Handler{Path: backend, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	server := httptest.NewServer(handler)
	defer server.Close()
	refOnly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			if strings.Contains(string(body), "want "+first) {
				http.Error(w, "Direct SHA fetch disabled", 400)
				return
			}
		}
		handler.ServeHTTP(w, r)
	}))
	defer refOnly.Close()
	authenticated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "ci-user" || password != strings.Repeat("t", 194) {
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

	for _, client := range []struct{ name, path, env string }{
		{"current", modern, ""},
		{"2.8.0", os.Getenv("DRONE_TEST_GIT_2_8"), "DRONE_TEST_GIT_2_8"},
		{"2.43.0", os.Getenv("DRONE_TEST_GIT_2_43"), "DRONE_TEST_GIT_2_43"},
	} {
		t.Run(client.name, func(t *testing.T) {
			if client.path == "" {
				t.Skipf("set %s to test Git %s", client.env, client.name)
			}
			clientPath, err := filepath.Abs(client.path)
			if err != nil {
				t.Fatal(err)
			}
			if client.name != "current" {
				out, err := exec.Command(clientPath, "--version").CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != "git version "+client.name {
					t.Fatalf("expected real Git %s: %v %s", client.name, err, out)
				}
			}
			for _, tc := range []struct {
				name, sha, ref                       string
				depth                                int
				fail, deny, auth, badToken, forceRef bool
			}{
				{name: "branch-tip", sha: tip, ref: "refs/heads/main"},
				{name: "branch-advanced", sha: first, ref: "refs/heads/main"},
				{name: "shallow-tip", sha: tip, ref: "refs/heads/main", depth: 1},
				{name: "shallow-advanced", sha: first, ref: "refs/heads/main", depth: 1},
				{name: "ref-fallback-unshallow", sha: first, ref: "refs/heads/main", depth: 1, forceRef: true},
				{name: "annotated-tag", sha: first, ref: "refs/tags/v1", depth: 1},
				{name: "pull-ref", sha: tip, ref: "refs/pull/7/head", depth: 1},
				{name: "authenticated-wrong-token", sha: tip, ref: "refs/heads/main", auth: true, badToken: true, fail: true},
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
					if tc.forceRef {
						remote = refOnly.URL + "/repo.git"
					}
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
					cmd.Env = []string{"HOME=" + home, "GIT_EXEC_PATH=" + clientExecPath(t, clientPath), "PATH=" + filepath.Dir(clientPath) + ":/usr/bin:/bin", "LC_ALL=C",
						"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "DRONE_REMOTE_URL=" + remote,
						"DRONE_COMMIT_SHA=" + tc.sha, "DRONE_COMMIT_REF=" + tc.ref}
					accountNetrc := "machine 127.0.0.1 login personal password do-not-change\n"
					authDir := filepath.Join(t.TempDir(), "auth")
					helperMarker := filepath.Join(t.TempDir(), "helper-called")
					if tc.auth {
						// Inherited helpers must neither supply credentials nor store the
						// Drone token, including Git 2.8's separate XDG config source.
						helper := filepath.Join(t.TempDir(), "credential-helper")
						if err := os.WriteFile(helper, []byte("#!/bin/sh\n: > "+quote(helperMarker)+"\n"), 0700); err != nil {
							t.Fatal(err)
						}
						xdg := t.TempDir()
						if err := os.Mkdir(filepath.Join(xdg, "git"), 0700); err != nil {
							t.Fatal(err)
						}
						systemConfig := filepath.Join(t.TempDir(), "gitconfig")
						for _, path := range []string{filepath.Join(home, ".gitconfig"), filepath.Join(xdg, "git", "config"), systemConfig} {
							if err := os.WriteFile(path, []byte("[credential]\n\thelper = "+helper+"\n"), 0600); err != nil {
								t.Fatal(err)
							}
						}
						// GIT_CONFIG_SYSTEM is supported by modern Git; Git 2.8 still
						// exercises HOME, XDG and command-line config isolation.
						cmd.Env = append(cmd.Env, "XDG_CONFIG_HOME="+xdg, "GIT_CONFIG_GLOBAL="+filepath.Join(home, ".gitconfig"),
							"GIT_CONFIG_NOSYSTEM=0", "GIT_CONFIG_SYSTEM="+systemConfig,
							"GIT_CONFIG_PARAMETERS="+quote("credential.helper="+helper),
							"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=credential.helper", "GIT_CONFIG_VALUE_0="+helper)
						if err := os.WriteFile(filepath.Join(home, ".netrc"), []byte(accountNetrc), 0600); err != nil {
							t.Fatal(err)
						}
						password := strings.Repeat("t", 194)
						if tc.badToken {
							password = strings.Repeat("x", 194)
						}
						if err := createCloneAuth(authDir, remote, "127.0.0.1", "ci-user", password); err != nil {
							t.Fatal(err)
						}
						cmd.Env = append(cmd.Env, "DRONE_LSF_CLONE_AUTH_DIR="+authDir, "GIT_CURL_VERBOSE=1", "GIT_TRACE=1", "GIT_ASKPASS=/must-not-run")
					}
					out, err := cmd.CombinedOutput()
					if ctx.Err() != nil || (err != nil) != tc.fail {
						t.Fatalf("unexpected result: %v (context %v)\n%s", err, ctx.Err(), out)
					}
					if tc.auth {
						if strings.Contains(string(out), "git: 'credential-' is not a git command") {
							t.Fatalf("empty credential helper invoked\n%s", out)
						}
						if _, err := os.Stat(helperMarker); !os.IsNotExist(err) {
							t.Fatal("inherited credential helper was invoked")
						}
						if _, err := os.Stat(authDir); !os.IsNotExist(err) {
							t.Fatal("clone credentials retained")
						}
						if strings.Contains(string(out), strings.Repeat("t", 194)) || strings.Contains(string(out), strings.Repeat("x", 194)) || strings.Contains(string(out), "Authorization:") {
							t.Fatal("credentials exposed in log")
						}
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
					if head, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); err != nil || strings.TrimSpace(string(head)) != tc.sha {
						t.Fatalf("HEAD must be detached at %s: %v %s", tc.sha, err, head)
					}
					wantContent := "second"
					if tc.sha == first {
						wantContent = "first"
					}
					if data, err := os.ReadFile(filepath.Join(dir, "version.txt")); err != nil || string(data) != wantContent {
						t.Fatalf("checkout content = %q, want %q: %v", data, wantContent, err)
					}
					if tc.forceRef && git(dir, "rev-list", "--count", "FETCH_HEAD") != "2" {
						t.Fatal("full-history fetch did not restore branch history")
					}
					if !strings.Contains(string(out), "[clone] Verified HEAD: "+tc.sha) {
						t.Fatalf("missing commit verification\n%s", out)
					}
					if tc.forceRef && (!strings.Contains(string(out), "Ref fetch succeeded") || !strings.Contains(string(out), "Full-history fetch succeeded")) {
						t.Fatalf("missing ref fallback / unshallow: %s", out)
					}
					if tc.auth {
						if data, err := os.ReadFile(filepath.Join(home, ".netrc")); err != nil || string(data) != accountNetrc {
							t.Fatalf("account netrc changed: %v", err)
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

func clientExecPath(t *testing.T, client string) string {
	t.Helper()
	cmd := exec.Command(client, "--exec-path")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

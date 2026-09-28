package basepath

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi"
)

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{"": "", "/": "", "drone": "/drone", "/drone/": "/drone", "/ci/drone": "/ci/drone"} {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Fatalf("%q: %q %v", in, got, err)
		}
	}
	for _, in := range []string{"/a/../b", "https://host/path", "/a?b", "/a%2fb", "/a//b", "/a b"} {
		if _, err := Normalize(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
}

func TestRoutingRedirectsAndCookies(t *testing.T) {
	for _, test := range []struct{ path, header, want string }{
		{"/drone/login", "", "/login"}, {"/login", "/drone", "/login"}, {"/drone/drone/repo", "", "/drone/repo"},
		{"/drone/repo", "/drone", "/drone/repo"}, {"/drone-other", "", "/drone-other"},
	} {
		t.Run(test.path+test.header, func(t *testing.T) {
			router := chi.NewRouter()
			router.Use(Middleware("/drone"))
			router.Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.want {
					t.Errorf("path %q want %q", r.URL.Path, test.want)
				}
				if r.URL.Query().Get("code") != "a+b" {
					t.Error("lost callback query")
				}
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "value", Path: "/", HttpOnly: true})
				http.SetCookie(w, &http.Cookie{Name: "state", Value: "value", Path: "/login"})
				http.Redirect(w, r, "/register", 303)
			}))
			r := httptest.NewRequest("GET", test.path+"?code=a%2Bb", nil)
			r.Header.Set("X-Forwarded-Prefix", test.header)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			if w.Header().Get("Location") != "/drone/register" {
				t.Fatal(w.Header())
			}
			cookies := w.Result().Cookies()
			if len(cookies) != 2 || cookies[0].Path != "/drone" || cookies[1].Path != "/drone/login" {
				t.Fatalf("cookies: %v", cookies)
			}
		})
	}
}

func TestRedirectTargets(t *testing.T) {
	for target, want := range map[string]string{"/": "/drone/", "/drone/login": "/drone/login", "https://gitea.example/login": "https://gitea.example/login", "//other.example/path": "//other.example/path"} {
		w := httptest.NewRecorder()
		Middleware("/drone")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, 302) })).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if got := w.Header().Get("Location"); got != want {
			t.Fatalf("%s: %s", target, got)
		}
	}
}

func TestStreamingIsNotBuffered(t *testing.T) {
	finish := make(chan struct{})
	s := httptest.NewServer(Middleware("/drone")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: ready\n\n"))
		w.(http.Flusher).Flush()
		<-finish
	})))
	defer func() { close(finish); s.Close() }()
	defer s.CloseClientConnections()
	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(s.URL + "/drone/api/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "data: ready\n" {
		t.Fatalf("stream: %q %v", line, err)
	}
}

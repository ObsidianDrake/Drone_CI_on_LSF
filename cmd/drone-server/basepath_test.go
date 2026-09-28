package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/drone/drone/cmd/drone-server/config"
	"github.com/drone/drone/handler/basepath"
)

func TestGiteaBasePathCallback(t *testing.T) {
	t.Setenv("DRONE_SERVER_HOST", "192.168.1.102:8083")
	t.Setenv("DRONE_SERVER_PROTO", "http")
	t.Setenv("DRONE_SERVER_BASE_PATH", "/drone/")
	t.Setenv("DRONE_GITEA_SERVER", "https://gitea.example")
	t.Setenv("DRONE_GITEA_CLIENT_ID", "test-client")
	t.Setenv("DRONE_GITEA_CLIENT_SECRET", "test-secret")
	t.Setenv("DRONE_GITEA_REDIRECT_URL", "")
	t.Setenv("DRONE_SERVER_PROXY_HOST", "")
	t.Setenv("DRONE_SERVER_PROXY_PROTO", "")
	c, err := config.Environ()
	if err != nil {
		t.Fatal(err)
	}
	want := "http://192.168.1.102:8083/drone"
	if c.Server.Addr != want || c.Proxy.Addr != want || provideSystem(c).Link != want {
		t.Fatalf("incorrect public URLs: %s %s", c.Server.Addr, c.Proxy.Addr)
	}
	h := basepath.Middleware(c.Server.BasePath)(provideGiteaLogin(c).Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected callback") })))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://192.168.1.102:8083/drone/login", nil))
	location, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Host != "gitea.example" || location.Query().Get("redirect_uri") != want+"/login" {
		t.Fatalf("OAuth redirect: %s", location)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Path != "/drone" {
			t.Fatalf("OAuth cookie path: %s", cookie.Path)
		}
	}
}

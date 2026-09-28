package user

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drone/go-scm/scm/driver/gitea"
	"github.com/drone/go-scm/scm/transport/oauth2"
)

func TestGiteaAdminProfile(t *testing.T) {
	for _, tc := range []struct {
		name, body, fallback string
		status               int
		admin, fail          bool
	}{
		{name: "admin", body: `{"login":"alice","is_admin":true}`, admin: true},
		{name: "ordinary", body: `{"login":"alice","is_admin":false}`},
		{name: "fallback", body: `{"login":"alice","is_admin":false}`, fallback: "Alice", admin: true},
		{name: "other fallback", body: `{"login":"alice","is_admin":false}`, fallback: "bob"},
		{name: "username", body: `{"username":"alice","is_admin":true}`, admin: true},
		{name: "missing flag", body: `{"login":"alice"}`, fail: true},
		{name: "missing login", body: `{"is_admin":true}`, fail: true},
		{name: "invalid", body: `not json`, fail: true},
		{name: "unauthorized", body: `{}`, status: 401, fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/gitea/api/v1/user" || r.Header.Get("Authorization") != "Bearer access-token" {
					t.Errorf("incorrect profile request: path=%s", r.URL.Path)
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				w.Write([]byte(tc.body))
			}))
			defer server.Close()
			client, err := gitea.New(server.URL + "/gitea")
			if err != nil {
				t.Fatal(err)
			}
			client.Client = &http.Client{Transport: &oauth2.Transport{Scheme: oauth2.SchemeBearer, Source: oauth2.ContextTokenSource()}}
			service := NewWithAdminFallback(client, nil, tc.fallback)
			got, err := service.Find(context.Background(), "access-token", "refresh-token")
			if tc.fail {
				if err == nil {
					t.Fatal("accepted invalid profile")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Login != "alice" || got.Admin != tc.admin {
				t.Fatalf("unexpected login/admin: %s %v", got.Login, got.Admin)
			}
		})
	}
}

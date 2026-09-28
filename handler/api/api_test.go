package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/monitor"
)

type guestSession struct{ core.Session }

func (guestSession) Get(*http.Request) (*core.User, error) { return nil, nil }

func TestHandlerMonitoringRoute(t *testing.T) {
	for _, test := range []struct {
		name   string
		trends *monitor.Service
		status int
	}{
		{"without monitoring", nil, http.StatusNotFound},
		{"with monitoring", &monitor.Service{Enabled: true}, http.StatusUnauthorized},
		{"without LSF", &monitor.Service{Enabled: false}, http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := Server{System: &core.System{}, Session: guestSession{}, Trends: test.trends}
			// Construct the complete router to catch middleware registered after routes.
			handler := server.Handler()
			req := httptest.NewRequest(http.MethodGet, "/monitor/trends", nil)
			req.Header.Set("Origin", "http://example.com")
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", res.Code, test.status, res.Body.String())
			}
			if res.Header().Get("Access-Control-Allow-Origin") == "" {
				t.Fatal("response is missing CORS headers")
			}
		})
	}
}

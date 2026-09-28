//go:build !oss
// +build !oss

package crons

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/mock"
	"github.com/go-chi/chi"
	"github.com/golang/mock/gomock"
)

func TestEmptyCronListIsArray(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	repos := mock.NewMockRepositoryStore(ctrl)
	repos.EXPECT().FindName(gomock.Any(), "test", "repo").Return(&core.Repository{ID: 1}, nil)
	crons := mock.NewMockCronStore(ctrl)
	crons.EXPECT().List(gomock.Any(), int64(1)).Return(nil, nil)
	route := new(chi.Context)
	route.URLParams.Add("owner", "test")
	route.URLParams.Add("name", "repo")
	r := httptest.NewRequest("GET", "/", nil)
	r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, route))
	w := httptest.NewRecorder()
	HandleList(repos, crons)(w, r)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

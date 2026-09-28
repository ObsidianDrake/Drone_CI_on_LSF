// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Drone Non-Commercial License
// that can be found in the LICENSE file.

package repos

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/errors"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/mock"

	"github.com/go-chi/chi"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
)

func TestUpdate(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	repo := &core.Repository{
		ID:         1,
		UserID:     1,
		Namespace:  "octocat",
		Name:       "hello-world",
		Slug:       "octocat/hello-world",
		Branch:     "master",
		Private:    false,
		Visibility: core.VisibilityPrivate,
		HTTPURL:    "https://github.com/octocat/hello-world.git",
		SSHURL:     "git@github.com:octocat/hello-world.git",
		Link:       "https://github.com/octocat/hello-world",
	}

	repoInput := map[string]interface{}{
		"visibility": core.VisibilityPublic,
	}

	checkUpdate := func(_ context.Context, updated *core.Repository) error {
		if got, want := updated.Visibility, core.VisibilityPublic; got != want {
			t.Errorf("Want repository visibility updated to %s, got %s", want, got)
		}
		return nil
	}

	repos := mock.NewMockRepositoryStore(controller)
	repos.EXPECT().FindName(gomock.Any(), "octocat", "hello-world").Return(repo, nil)
	repos.EXPECT().Update(gomock.Any(), repo).Return(nil).Do(checkUpdate)

	c := new(chi.Context)
	c.URLParams.Add("owner", "octocat")
	c.URLParams.Add("name", "hello-world")

	in := new(bytes.Buffer)
	json.NewEncoder(in).Encode(repoInput)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", in)
	r = r.WithContext(
		context.WithValue(r.Context(), chi.RouteCtxKey, c),
	)

	HandleUpdate(repos)(w, r)
	if got, want := w.Code, 200; want != got {
		t.Errorf("Want response code %d, got %d", want, got)
	}

	got, want := new(core.Repository), &core.Repository{
		ID:         1,
		UserID:     1,
		Namespace:  "octocat",
		Name:       "hello-world",
		Slug:       "octocat/hello-world",
		Branch:     "master",
		Private:    false,
		Visibility: core.VisibilityPublic,
		HTTPURL:    "https://github.com/octocat/hello-world.git",
		SSHURL:     "git@github.com:octocat/hello-world.git",
		Link:       "https://github.com/octocat/hello-world",
	}
	json.NewDecoder(w.Body).Decode(got)
	if diff := cmp.Diff(got, want); len(diff) > 0 {
		t.Errorf("Diff: %s", diff)
	}
}

// this test verifies that a 404 not found error is returned
// from the http.Handler if the named repository cannot be
// found in the database.
func TestUpdate_RepoNotFound(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	repos := mock.NewMockRepositoryStore(controller)
	repos.EXPECT().FindName(gomock.Any(), "octocat", "hello-world").Return(nil, errors.ErrNotFound)

	c := new(chi.Context)
	c.URLParams.Add("owner", "octocat")
	c.URLParams.Add("name", "hello-world")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", nil)
	r = r.WithContext(
		context.WithValue(r.Context(), chi.RouteCtxKey, c),
	)

	HandleUpdate(repos)(w, r)
	if got, want := w.Code, 404; want != got {
		t.Errorf("Want response code %d, got %d", want, got)
	}

	got, want := new(errors.Error), errors.ErrNotFound
	json.NewDecoder(w.Body).Decode(got)
	if diff := cmp.Diff(got, want); len(diff) != 0 {
		t.Errorf("Diff: %s", diff)
	}
}

// this test verifies that a 400 bad request error is
// returned from the http.Handler if the request body
// is invalid json.
func TestUpdate_InvalidInput(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	repo := &core.Repository{
		ID:         1,
		UserID:     1,
		Namespace:  "octocat",
		Name:       "hello-world",
		Slug:       "octocat/hello-world",
		Branch:     "master",
		Private:    false,
		Visibility: core.VisibilityPrivate,
		HTTPURL:    "https://github.com/octocat/hello-world.git",
		SSHURL:     "git@github.com:octocat/hello-world.git",
		Link:       "https://github.com/octocat/hello-world",
	}

	repos := mock.NewMockRepositoryStore(controller)
	repos.EXPECT().FindName(gomock.Any(), "octocat", "hello-world").Return(repo, nil)

	c := new(chi.Context)
	c.URLParams.Add("owner", "octocat")
	c.URLParams.Add("name", "hello-world")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader(""))
	r = r.WithContext(
		context.WithValue(r.Context(), chi.RouteCtxKey, c),
	)

	HandleUpdate(repos)(w, r)
	if got, want := w.Code, 400; want != got {
		t.Errorf("Want response code %d, got %d", want, got)
	}

	got, want := new(errors.Error), errors.New("EOF")
	json.NewDecoder(w.Body).Decode(got)
	if diff := cmp.Diff(got, want); len(diff) != 0 {
		t.Errorf("Diff: %s", diff)
	}
}

// this test verifies that a 500 internal server error is
// returned from the http.Handler if the repository updates
// cannot be persisted to the database.
func TestUpdate_UpdateFailed(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	repo := &core.Repository{
		ID:         1,
		UserID:     1,
		Namespace:  "octocat",
		Name:       "hello-world",
		Slug:       "octocat/hello-world",
		Branch:     "master",
		Private:    false,
		Visibility: core.VisibilityPrivate,
		HTTPURL:    "https://github.com/octocat/hello-world.git",
		SSHURL:     "git@github.com:octocat/hello-world.git",
		Link:       "https://github.com/octocat/hello-world",
	}

	repoInput := map[string]interface{}{
		"visibility": core.VisibilityPublic,
	}

	repos := mock.NewMockRepositoryStore(controller)
	repos.EXPECT().FindName(gomock.Any(), "octocat", "hello-world").Return(repo, nil)
	repos.EXPECT().Update(gomock.Any(), repo).Return(errors.ErrNotFound)

	c := new(chi.Context)
	c.URLParams.Add("owner", "octocat")
	c.URLParams.Add("name", "hello-world")

	in := new(bytes.Buffer)
	json.NewEncoder(in).Encode(repoInput)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", in)
	r = r.WithContext(
		context.WithValue(r.Context(), chi.RouteCtxKey, c),
	)

	HandleUpdate(repos)(w, r)
	if got, want := w.Code, 500; want != got {
		t.Errorf("Want response code %d, got %d", want, got)
	}

	got, want := new(errors.Error), errors.ErrNotFound
	json.NewDecoder(w.Body).Decode(got)
	if diff := cmp.Diff(got, want); len(diff) != 0 {
		t.Errorf("Diff: %s", diff)
	}
}

func TestUpdateAutoCancelRunning(t *testing.T) {
	controller := gomock.NewController(t)
	defer controller.Finish()

	repo := &core.Repository{
		ID:            1,
		UserID:        1,
		Namespace:     "octocat",
		Name:          "hello-world",
		Slug:          "octocat/hello-world",
		Branch:        "master",
		Private:       false,
		Visibility:    core.VisibilityPrivate,
		HTTPURL:       "https://github.com/octocat/hello-world.git",
		SSHURL:        "git@github.com:octocat/hello-world.git",
		Link:          "https://github.com/octocat/hello-world",
		CancelRunning: false,
	}

	repoInput := map[string]interface{}{
		"auto_cancel_running": true,
		"visibility":          core.VisibilityPrivate,
	}

	shouldBeValue := true
	checkUpdate := func(_ context.Context, updated *core.Repository) error {
		if got, want := updated.CancelRunning, shouldBeValue; got != want {
			t.Errorf("Want repository visibility updated to %v, got %v", want, got)
		}
		return nil
	}

	repos := mock.NewMockRepositoryStore(controller)
	repos.EXPECT().FindName(gomock.Any(), "octocat", "hello-world").Return(repo, nil)
	repos.EXPECT().Update(gomock.Any(), repo).Return(nil).Do(checkUpdate)

	c := new(chi.Context)
	c.URLParams.Add("owner", "octocat")
	c.URLParams.Add("name", "hello-world")

	in := new(bytes.Buffer)
	json.NewEncoder(in).Encode(repoInput)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", in)
	r = r.WithContext(
		context.WithValue(r.Context(), chi.RouteCtxKey, c),
	)

	HandleUpdate(repos)(w, r)
	if got, want := w.Code, 200; want != got {
		t.Errorf("Want response code %d, got %d", want, got)
	}

	got, want := new(core.Repository), &core.Repository{
		ID:            1,
		UserID:        1,
		Namespace:     "octocat",
		Name:          "hello-world",
		Slug:          "octocat/hello-world",
		Branch:        "master",
		Private:       false,
		Visibility:    core.VisibilityPrivate,
		HTTPURL:       "https://github.com/octocat/hello-world.git",
		SSHURL:        "git@github.com:octocat/hello-world.git",
		Link:          "https://github.com/octocat/hello-world",
		CancelRunning: true,
	}
	json.NewDecoder(w.Body).Decode(got)
	if diff := cmp.Diff(got, want); len(diff) > 0 {
		t.Errorf("Diff: %s", diff)
	}
}

func TestUpdateLSFJobInfoAdminOnly(t *testing.T) {
	for _, tc := range []struct {
		name                string
		admin, before, next bool
		code                int
	}{
		{"admin disables", true, false, true, 200}, {"admin enables", true, true, false, 200},
		{"nonadmin disables", false, false, true, 403}, {"nonadmin enables", false, true, false, 403},
		{"nonadmin unchanged", false, true, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			repo := &core.Repository{LSFJobInfoDisabled: tc.before}
			repos := mock.NewMockRepositoryStore(ctrl)
			repos.EXPECT().FindName(gomock.Any(), "test", "repo").Return(repo, nil)
			if tc.code == 200 {
				repos.EXPECT().Update(gomock.Any(), repo).Do(func(_ context.Context, r *core.Repository) {
					if r.LSFJobInfoDisabled != tc.next {
						t.Fatal("setting not updated")
					}
				}).Return(nil)
			}
			c := new(chi.Context)
			c.URLParams.Add("owner", "test")
			c.URLParams.Add("name", "repo")
			body, _ := json.Marshal(map[string]bool{"lsf_job_info_disabled": tc.next})
			r := httptest.NewRequest("PATCH", "/", bytes.NewReader(body))
			r = r.WithContext(request.WithUser(context.WithValue(r.Context(), chi.RouteCtxKey, c), &core.User{Admin: tc.admin}))
			w := httptest.NewRecorder()
			HandleUpdate(repos)(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.code == 403 && repo.LSFJobInfoDisabled != tc.before {
				t.Fatal("unauthorized mutation")
			}
		})
	}
}

func TestTimeoutHoursValidation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		admin      bool
		code       int
		minutes    int64
	}{
		{"one hour", `{"timeout_hours":1}`, true, 200, 60},
		{"multiple hours", `{"timeout_hours":12}`, true, 200, 720},
		{"empty", `{"timeout_hours":""}`, true, 400, 0},
		{"null", `{"timeout_hours":null}`, true, 400, 0},
		{"zero", `{"timeout_hours":0}`, true, 400, 0},
		{"negative", `{"timeout_hours":-1}`, true, 400, 0},
		{"fraction", `{"timeout_hours":1.5}`, true, 400, 0},
		{"string", `{"timeout_hours":"2"}`, true, 400, 0},
		{"overflow", `{"timeout_hours":2562048}`, true, 400, 0},
		{"conflict", `{"timeout_hours":2,"timeout":60}`, true, 400, 0},
		{"legacy minutes", `{"timeout":30}`, true, 200, 30},
		{"nonadmin change", `{"timeout_hours":2}`, false, 403, 0},
		{"nonadmin unchanged", `{"timeout_hours":1}`, false, 200, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			repo := &core.Repository{Timeout: 60}
			repos := mock.NewMockRepositoryStore(ctrl)
			repos.EXPECT().FindName(gomock.Any(), "test", "repo").Return(repo, nil)
			if tc.code == 200 {
				repos.EXPECT().Update(gomock.Any(), repo).Do(func(_ context.Context, r *core.Repository) {
					if r.Timeout != tc.minutes {
						t.Fatalf("timeout=%d want %d", r.Timeout, tc.minutes)
					}
				}).Return(nil)
			}
			c := new(chi.Context)
			c.URLParams.Add("owner", "test")
			c.URLParams.Add("name", "repo")
			r := httptest.NewRequest("PATCH", "/", strings.NewReader(tc.body))
			r = r.WithContext(request.WithUser(context.WithValue(r.Context(), chi.RouteCtxKey, c), &core.User{Admin: tc.admin}))
			w := httptest.NewRecorder()
			HandleUpdate(repos)(w, r)
			if w.Code != tc.code {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.code != 200 && repo.Timeout != 60 {
				t.Fatal("invalid input changed timeout")
			}
		})
	}
}

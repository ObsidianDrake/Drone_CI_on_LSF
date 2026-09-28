// Copyright 2019 Drone.IO Inc. All rights reserved.
// Use of this source code is governed by the Drone Non-Commercial License
// that can be found in the LICENSE file.

package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/basepath"
	"github.com/drone/drone/mock"
	"github.com/drone/go-login/login"
	"github.com/golang/mock/gomock"
)

type admissionFunc func(context.Context, *core.User) error

func (f admissionFunc) Admit(ctx context.Context, u *core.User) error { return f(ctx, u) }

type passthroughLogin struct{}

func (passthroughLogin) Handler(next http.Handler) http.Handler { return next }

func TestLoginSkipsProfileRegistration(t *testing.T) {
	for _, test := range []struct {
		name                             string
		fresh, inactive, machine, denied bool
	}{
		{name: "new user", fresh: true}, {name: "existing user without email"},
		{name: "inactive", inactive: true}, {name: "machine", machine: true}, {name: "admission denied", denied: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			users := mock.NewMockUserStore(ctrl)
			userz := mock.NewMockUserService(ctrl)
			session := mock.NewMockSession(ctrl)
			syncer := mock.NewMockSyncer(ctrl)
			sender := mock.NewMockWebhookSender(ctrl)
			user := &core.User{Login: "gitea-user", Active: !test.inactive, Machine: test.machine, Created: time.Now().Unix(), Synced: time.Now().Unix()}
			userz.EXPECT().Find(gomock.Any(), "access", "refresh").Return(&core.User{Login: user.Login}, nil)
			lookupErr := error(nil)
			if test.fresh {
				lookupErr = sql.ErrNoRows
			}
			users.EXPECT().FindLogin(gomock.Any(), user.Login).Return(user, lookupErr)
			synced := make(chan struct{})
			if test.fresh {
				users.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				sender.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil)
				syncer.EXPECT().Sync(gomock.Any(), gomock.Any()).Do(func(context.Context, *core.User) { close(synced) }).Return(nil, nil)
			}
			blocked := test.inactive || test.machine || test.denied
			if !blocked {
				users.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
				session.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			}
			admission := admissionFunc(func(context.Context, *core.User) error {
				if test.denied {
					return errors.New("denied")
				}
				return nil
			})
			h := basepath.Middleware("/drone")(HandleLogin(users, userz, syncer, session, admission, sender))
			r := httptest.NewRequest("GET", "/drone/login?code=test", nil)
			r = r.WithContext(login.WithToken(r.Context(), &login.Token{Access: "access", Refresh: "refresh"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			location := w.Header().Get("Location")
			if blocked {
				if !strings.HasPrefix(location, "/drone/login/error?") {
					t.Fatalf("blocked account redirected to %s", location)
				}
			} else if w.Code != 303 || location != "/drone/" {
				t.Fatalf("login: %d %s", w.Code, location)
			}
			if test.fresh {
				select {
				case <-synced:
				case <-time.After(time.Second):
					t.Fatal("repository sync was not started")
				}
			}
		})
	}
}

func TestRegisterRedirectsHome(t *testing.T) {
	for _, base := range []string{"", "/drone"} {
		s := Server{BasePath: base, Login: passthroughLogin{}}
		h := basepath.Middleware(base)(s.Handler())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", base+"/register", nil))
		if w.Code != 303 || w.Header().Get("Location") != base+"/" {
			t.Fatalf("register: %d %s", w.Code, w.Header().Get("Location"))
		}
	}
}

type adminSyncUserService struct{ core.UserService }

func (adminSyncUserService) SyncAdmin() bool { return true }

func TestLoginSynchronizesAdmin(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		old, remote, fresh, updateFail bool
	}{
		{name: "grant", remote: true}, {name: "revoke", old: true},
		{name: "new admin", fresh: true, remote: true}, {name: "new ordinary", fresh: true},
		{name: "failed revocation write", old: true, updateFail: true},
		{name: "failed grant write", remote: true, updateFail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			users := mock.NewMockUserStore(ctrl)
			remote := mock.NewMockUserService(ctrl)
			sessions := mock.NewMockSession(ctrl)
			syncer := mock.NewMockSyncer(ctrl)
			sender := mock.NewMockWebhookSender(ctrl)
			remote.EXPECT().Find(gomock.Any(), "access", "refresh").Return(&core.User{Login: "alice", Admin: tc.remote}, nil)
			user := &core.User{Login: "alice", Admin: tc.old, Active: true, Synced: time.Now().Unix()}
			var lookupErr error
			if tc.fresh {
				lookupErr = sql.ErrNoRows
			}
			users.EXPECT().FindLogin(gomock.Any(), "alice").Return(user, lookupErr)
			check := func(_ context.Context, u *core.User) {
				if u.Admin != tc.remote {
					t.Errorf("admin=%v want %v", u.Admin, tc.remote)
				}
			}
			synced := make(chan struct{})
			if tc.fresh {
				users.EXPECT().Create(gomock.Any(), gomock.Any()).Do(check).Return(nil)
				sender.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil)
				syncer.EXPECT().Sync(gomock.Any(), gomock.Any()).Do(func(context.Context, *core.User) { close(synced) }).Return(nil, nil)
			}
			var updateErr error
			if tc.updateFail {
				updateErr = errors.New("database unavailable")
			}
			users.EXPECT().Update(gomock.Any(), gomock.Any()).Do(check).Return(updateErr)
			if !tc.updateFail {
				sessions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
			}
			h := HandleLogin(users, adminSyncUserService{remote}, syncer, sessions, admissionFunc(func(context.Context, *core.User) error { return nil }), sender)
			r := httptest.NewRequest("GET", "/login", nil)
			r = r.WithContext(login.WithToken(r.Context(), &login.Token{Access: "access", Refresh: "refresh"}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			location := w.Header().Get("Location")
			if tc.updateFail {
				if !strings.HasPrefix(location, "/login/error?") {
					t.Fatalf("location=%s", location)
				}
			} else if location != "/" {
				t.Fatalf("location=%s", location)
			}
			if tc.fresh {
				select {
				case <-synced:
				case <-time.After(time.Second):
					t.Fatal("sync not started")
				}
			}
		})
	}
}

func TestDeploymentsRedirectsBuilds(t *testing.T) {
	for _, base := range []string{"", "/drone"} {
		s := Server{BasePath: base, Login: passthroughLogin{}}
		h := basepath.Middleware(base)(s.Handler())
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", base+"/DRC-1-04/perl_script/deployments", nil))
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != base+"/DRC-1-04/perl_script" {
			t.Fatalf("redirect %d %s", w.Code, w.Header().Get("Location"))
		}
	}
}

// Copyright 2019 Drone IO, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package user

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/drone/drone/core"
	"github.com/drone/go-scm/scm"
)

type service struct {
	client        *scm.Client
	renew         core.Renewer
	adminFallback string
}

// New returns a new User service that provides access to
// user data from the source code management system.
func New(client *scm.Client, renew core.Renewer) core.UserService {
	return &service{client: client, renew: renew}
}

// NewWithAdminFallback preserves an explicitly configured local administrator.
func NewWithAdminFallback(client *scm.Client, renew core.Renewer, login string) core.UserService {
	return &service{client: client, renew: renew, adminFallback: login}
}

// SyncAdmin declares that Find returns an authoritative administrator status.
func (s *service) SyncAdmin() bool { return s.client.Driver == scm.DriverGitea }

func (s *service) Find(ctx context.Context, access, refresh string) (*core.User, error) {
	ctx = context.WithValue(ctx, scm.TokenKey{}, &scm.Token{
		Token:   access,
		Refresh: refresh,
	})
	if s.SyncAdmin() {
		return s.findGitea(ctx)
	}
	src, _, err := s.client.Users.Find(ctx)
	if err != nil {
		return nil, err
	}
	return convert(src), nil
}

func (s *service) FindLogin(ctx context.Context, user *core.User, login string) (*core.User, error) {
	err := s.renew.Renew(ctx, user, false)
	if err != nil {
		return nil, err
	}

	ctx = context.WithValue(ctx, scm.TokenKey{}, &scm.Token{
		Token:   user.Token,
		Refresh: user.Refresh,
	})
	src, _, err := s.client.Users.FindLogin(ctx, login)
	if err != nil {
		return nil, err
	}
	return convert(src), nil
}

func convert(src *scm.User) *core.User {
	dst := &core.User{
		Login:  src.Login,
		Email:  src.Email,
		Avatar: src.Avatar,
	}
	if !src.Created.IsZero() {
		dst.Created = src.Created.Unix()
	}
	if !src.Updated.IsZero() {
		dst.Updated = src.Updated.Unix()
	}
	return dst
}

// Read the authenticated user's profile directly: this pinned go-scm version
// discards Gitea's site-wide is_admin field when converting to scm.User.
func (s *service) findGitea(ctx context.Context) (*core.User, error) {
	res, err := s.client.Do(ctx, &scm.Request{Method: http.MethodGet, Path: "api/v1/user"})
	if err != nil {
		return nil, fmt.Errorf("cannot fetch Gitea account for admin synchronization")
	}
	defer res.Body.Close()
	if res.Status != http.StatusOK {
		return nil, fmt.Errorf("Gitea account lookup returned HTTP %d", res.Status)
	}
	var profile struct {
		Login    string `json:"login"`
		Username string `json:"username"`
		Avatar   string `json:"avatar_url"`
		Email    string `json:"email"`
		Admin    *bool  `json:"is_admin"`
	}
	if err := json.NewDecoder(res.Body).Decode(&profile); err != nil {
		return nil, fmt.Errorf("invalid Gitea account response")
	}
	if profile.Username != "" {
		profile.Login = profile.Username
	}
	if profile.Login == "" || profile.Admin == nil {
		return nil, fmt.Errorf("Gitea account response missing login or is_admin; cannot synchronize administrator status")
	}
	admin := *profile.Admin || (s.adminFallback != "" && strings.EqualFold(profile.Login, s.adminFallback))
	return &core.User{Login: profile.Login, Avatar: profile.Avatar, Email: profile.Email, Admin: admin}, nil
}

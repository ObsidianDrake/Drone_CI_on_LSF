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

package repos

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/render"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/logger"

	"github.com/go-chi/chi"
)

const maxTimeoutHours = int64((1<<63 - 1) / time.Hour)

type (
	repositoryInput struct {
		TimeoutHours       json.RawMessage `json:"timeout_hours"`
		Visibility         *string         `json:"visibility"`
		Config             *string         `json:"config_path"`
		Trusted            *bool           `json:"trusted"`
		LSFJobInfoDisabled *bool           `json:"lsf_job_info_disabled"`
		Protected          *bool           `json:"protected"`
		IgnoreForks        *bool           `json:"ignore_forks"`
		IgnorePulls        *bool           `json:"ignore_pull_requests"`
		CancelPulls        *bool           `json:"auto_cancel_pull_requests"`
		CancelPush         *bool           `json:"auto_cancel_pushes"`
		CancelRunning      *bool           `json:"auto_cancel_running"`
		Timeout            *int64          `json:"timeout"`
		Throttle           *int64          `json:"throttle"`
		Counter            *int64          `json:"counter"`
	}
)

// HandleUpdate returns an http.HandlerFunc that processes http
// requests to update the repository details.
func HandleUpdate(repos core.RepositoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			owner = chi.URLParam(r, "owner")
			name  = chi.URLParam(r, "name")
			slug  = owner + "/" + name
		)
		user, _ := request.UserFrom(r.Context())

		repo, err := repos.FindName(r.Context(), owner, name)
		if err != nil {
			render.NotFound(w, err)
			logger.FromRequest(r).
				WithError(err).
				WithField("repository", slug).
				Debugln("api: repository not found")
			return
		}

		in := new(repositoryInput)
		err = json.NewDecoder(r.Body).Decode(in)
		if err != nil {
			render.BadRequest(w, err)
			logger.FromRequest(r).
				WithError(err).
				WithField("repository", slug).
				Debugln("api: cannot unmarshal json input")
			return
		}

		if len(in.TimeoutHours) > 0 {
			var hours int64
			if in.Timeout != nil || json.Unmarshal(in.TimeoutHours, &hours) != nil || hours < 1 || hours > maxTimeoutHours {
				render.BadRequestf(w, "Timeout must be an integer from 1 to %d hours; do not also send timeout", maxTimeoutHours)
				return
			}
			minutes := hours * 60
			in.Timeout = &minutes
		}
		if in.Timeout != nil {
			if *in.Timeout < 1 || *in.Timeout > maxTimeoutHours*60 {
				render.BadRequestf(w, "Timeout is outside the supported range")
				return
			}
			if (user == nil || !user.Admin) && *in.Timeout != repo.Timeout {
				http.Error(w, "Only Drone administrators can change timeout", http.StatusForbidden)
				return
			}
		}

		if in.LSFJobInfoDisabled != nil && *in.LSFJobInfoDisabled != repo.LSFJobInfoDisabled {
			if user == nil || !user.Admin {
				http.Error(w, "Only Drone administrators can change LSF job information settings", http.StatusForbidden)
				return
			}
			repo.LSFJobInfoDisabled = *in.LSFJobInfoDisabled
		}

		if in.Visibility != nil {
			repo.Visibility = *in.Visibility
		}
		if in.Config != nil {
			repo.Config = *in.Config
		}
		if in.Protected != nil {
			repo.Protected = *in.Protected
		}
		if in.IgnoreForks != nil {
			repo.IgnoreForks = *in.IgnoreForks
		}
		if in.IgnorePulls != nil {
			repo.IgnorePulls = *in.IgnorePulls
		}
		if in.CancelPulls != nil {
			repo.CancelPulls = *in.CancelPulls
		}
		if in.CancelPush != nil {
			repo.CancelPush = *in.CancelPush
		}
		if in.CancelRunning != nil {
			repo.CancelRunning = *in.CancelRunning
		}

		//
		// system administrator only
		//
		if user != nil && user.Admin {
			if in.Trusted != nil {
				repo.Trusted = *in.Trusted
			}
			if in.Timeout != nil {
				repo.Timeout = *in.Timeout
			}
			if in.Throttle != nil {
				repo.Throttle = *in.Throttle
			}
			if in.Counter != nil {
				repo.Counter = *in.Counter
			}
		}

		// // right now the only repository field that a user
		// // can update is the visibility field.
		// if govalidator.IsIn(in.Visibility,
		// 	core.VisibilityInternal,
		// 	core.VisibilityPrivate,
		// 	core.VisibilityPublic,
		// ) {
		// 	repo.Visibility = in.Visibility
		// }

		err = repos.Update(r.Context(), repo)
		if err != nil {
			render.InternalError(w, err)
			logger.FromRequest(r).
				WithError(err).
				WithField("repository", slug).
				Warnln("api: cannot update repository")
			return
		}

		render.JSON(w, repo, 200)
	}
}

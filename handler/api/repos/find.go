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
	"net/http"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/render"
	"github.com/drone/drone/handler/api/request"
)

// HandleFind returns an http.HandlerFunc that writes the
// json-encoded repository details to the response body.
func HandleFind(repos core.RepositoryStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		repo, _ := request.RepoFrom(ctx)
		perm, _ := request.PermFrom(ctx)
		repo.Perms = perm
		if store, ok := repos.(core.RepositoryBuildNumberStore); ok {
			var err error
			repo.NextBuildNumberEditable, err = store.CanSetNextBuildNumber(ctx, repo.ID)
			if err != nil {
				render.InternalError(w, err)
				return
			}
		}
		render.JSON(w, repo, 200)
	}
}

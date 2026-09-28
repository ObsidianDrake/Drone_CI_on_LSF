package monitor

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/render"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/monitor"
)

type Reader interface {
	Repositories(context.Context) ([]monitor.Repository, error)
	Read(context.Context, []monitor.Repository, time.Time, int) (*monitor.Result, error)
}

func Handle(store Reader, remote core.RepositoryService, lsfEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user, ok := request.UserFrom(r.Context())
		if !ok || user == nil {
			render.Unauthorized(w, fmt.Errorf("sign in to view workload trends"))
			return
		}
		if !user.Active {
			render.Forbidden(w, fmt.Errorf("active account required"))
			return
		}
		hours := 6
		if text := r.URL.Query().Get("hours"); text != "" {
			v, err := strconv.Atoi(text)
			if err != nil || v < 1 || v > 168 {
				render.BadRequest(w, fmt.Errorf("hours must be between 1 and 168"))
				return
			}
			hours = v
		}
		wanted := map[int64]bool{}
		raw, explicit := r.URL.Query()["repos"]
		if explicit {
			for _, text := range strings.Split(strings.Join(raw, ","), ",") {
				if text == "" {
					continue
				}
				id, err := strconv.ParseInt(text, 10, 64)
				if err != nil || id < 1 {
					render.BadRequest(w, fmt.Errorf("invalid repository selection"))
					return
				}
				wanted[id] = true
			}
			if len(wanted) > 12 {
				render.BadRequest(w, fmt.Errorf("select at most 12 repositories"))
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		repos, err := store.Repositories(ctx)
		if err != nil {
			render.InternalError(w, err)
			return
		}
		visible := []monitor.Repository{}
		selected := []monitor.Repository{}
		permissionErrors := false
		for _, repo := range repos {
			allowed := user.Admin || repo.Visibility == core.VisibilityPublic || repo.Visibility == core.VisibilityInternal
			if !allowed {
				// Recheck private access using SCM permissions on every request, fail closed.
				perm, err := remote.FindPerm(ctx, user, repo.Slug)
				if err != nil {
					permissionErrors = true
					continue
				}
				allowed = perm != nil && perm.Read
			}
			if !allowed {
				continue
			}
			visible = append(visible, repo)
			if (explicit && wanted[repo.ID]) || (!explicit && len(selected) < 6) {
				selected = append(selected, repo)
			}
		}
		result, err := store.Read(ctx, selected, time.Now(), hours)
		if err != nil {
			render.InternalError(w, err)
			return
		}
		render.JSON(w, struct {
			*monitor.Result
			Repositories     []monitor.Repository `json:"repositories"`
			LSFEnabled       bool                 `json:"lsf_enabled"`
			PermissionErrors bool                 `json:"permission_errors,omitempty"`
		}{result, visible, lsfEnabled, permissionErrors}, 200)
	}
}

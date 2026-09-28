package builds

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/drone/drone/core"
	"github.com/drone/drone/handler/api/render"
	"github.com/drone/drone/handler/api/request"
	"github.com/drone/drone/logger"
	"github.com/go-chi/chi"
)

// HandleHistory previews and deletes completed history, exclusively for system admins.
func HandleHistory(repos core.RepositoryStore, builds core.BuildStore, logs core.LogStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _ := request.UserFrom(r.Context())
		if user == nil || !user.Admin {
			render.Forbidden(w, fmt.Errorf("Drone administrator access required"))
			return
		}
		var input struct {
			core.HistoryFilter
			Token  string `json:"token"`
			Commit bool   `json:"commit"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			render.BadRequest(w, err)
			return
		}
		if err := decoder.Decode(new(interface{})); err != io.EOF {
			render.BadRequest(w, fmt.Errorf("expected one JSON object"))
			return
		}
		if input.Before < 0 || ((input.Before > 0 && len(input.Numbers) > 0) || (input.Before == 0 && len(input.Numbers) == 0 && !input.NonSuccess)) || len(input.Numbers) > 100 || (input.Before > 0 && input.NonSuccess) || (input.Commit && input.Token == "") || (!input.Commit && input.Token != "") {
			render.BadRequest(w, fmt.Errorf("specify build numbers, a positive before number, or non_success; deletion requires a preview token"))
			return
		}
		sort.Slice(input.Numbers, func(i, j int) bool { return input.Numbers[i] < input.Numbers[j] })
		for i, n := range input.Numbers {
			if n <= 0 || (i > 0 && input.Numbers[i-1] == n) {
				render.BadRequest(w, fmt.Errorf("build numbers must be unique positive integers"))
				return
			}
		}
		repo, err := repos.FindName(r.Context(), chi.URLParam(r, "owner"), chi.URLParam(r, "name"))
		if err != nil {
			render.NotFound(w, err)
			return
		}
		history, ok := builds.(core.HistoryStore)
		if !ok {
			render.InternalError(w, fmt.Errorf("history deletion is unavailable for this build store"))
			return
		}
		result, err := history.DeleteHistory(r.Context(), repo.ID, input.HistoryFilter, input.Token)
		if errors.Is(err, core.ErrHistoryChanged) {
			render.JSON(w, map[string]string{"message": err.Error()}, http.StatusConflict)
			return
		}
		if err != nil {
			render.InternalError(w, err)
			return
		}
		// SQL logs are removed transactionally. Also remove externally stored log objects.
		failed := 0
		if input.Commit && logs != nil {
			for _, step := range result.StepIDs {
				if err := logs.Delete(r.Context(), step); err != nil {
					failed++
					logger.FromRequest(r).WithError(err).WithField("step_id", step).Error("history deletion: external log cleanup failed")
				}
			}
		}
		render.JSON(w, struct {
			*core.HistoryPreview
			CleanupFailures int `json:"cleanup_failures,omitempty"`
		}{result, failed}, http.StatusOK)
	}
}

package build

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/shared/db"
)

func (s *buildStore) DeleteHistory(ctx context.Context, repo int64, filter core.HistoryFilter, token string) (*core.HistoryPreview, error) {
	out := &core.HistoryPreview{Numbers: []int64{}}
	err := s.db.Update(func(tx db.Execer, binder db.Binder) error {
		params := map[string]interface{}{"repo": repo, "before": filter.Before}
		condition := "build_repo_id = :repo AND build_status IN ('success','failure','error','killed','skipped')"
		if filter.Before > 0 {
			condition += " AND build_number < :before"
		} else if len(filter.Numbers) > 0 {
			names := []string{}
			for i, number := range filter.Numbers {
				key := fmt.Sprintf("n%d", i)
				names = append(names, ":"+key)
				params[key] = number
			}
			condition += " AND build_number IN (" + strings.Join(names, ",") + ")"
		}
		if filter.Before <= 0 && len(filter.Numbers) == 0 && !filter.NonSuccess {
			return fmt.Errorf("history selection required")
		}
		if filter.NonSuccess {
			condition += " AND build_status <> 'success'"
		}
		// A canceled build can still have workers tearing down; preserve it until they finish.
		condition += " AND NOT EXISTS (SELECT 1 FROM stages WHERE stage_build_id = build_id AND stage_status IN ('pending','running','waiting','blocked'))"
		condition += " AND NOT EXISTS (SELECT 1 FROM steps JOIN stages ON step_stage_id = stage_id WHERE stage_build_id = build_id AND step_status IN ('pending','running','waiting','blocked'))"
		query, args, err := binder.BindNamed("SELECT build_id, build_number, build_version, build_status FROM builds WHERE "+condition+" ORDER BY build_number", params)
		if err != nil {
			return err
		}
		if token != "" && s.db.Driver() != db.Sqlite {
			query += " FOR UPDATE"
		}
		rows, err := tx.Query(query, args...)
		if err != nil {
			return err
		}
		ids := []int64{}
		hash := sha256.New()
		fmt.Fprintf(hash, "%d:%v:%d:%t;", repo, filter.Numbers, filter.Before, filter.NonSuccess)
		for rows.Next() {
			var id, number, version int64
			var status string
			if err = rows.Scan(&id, &number, &version, &status); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			out.Numbers = append(out.Numbers, number)
			fmt.Fprintf(hash, "%d:%d:%d:%s;", id, number, version, status)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		out.Token = fmt.Sprintf("%x", hash.Sum(nil))
		if token == "" {
			return nil
		}
		if token != out.Token {
			return core.ErrHistoryChanged
		}
		for _, id := range ids {
			p := map[string]interface{}{"id": id}
			query, args, err = binder.BindNamed("SELECT step_id FROM steps JOIN stages ON step_stage_id = stage_id WHERE stage_build_id = :id", p)
			if err != nil {
				return err
			}
			rows, err = tx.Query(query, args...)
			if err != nil {
				return err
			}
			for rows.Next() {
				var step int64
				if err = rows.Scan(&step); err != nil {
					rows.Close()
					return err
				}
				out.StepIDs = append(out.StepIDs, step)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			// Repoint branch/PR indexes to remaining history before removing this build.
			query, args, err = binder.BindNamed(`SELECT COALESCE(MAX(b.build_id),0) FROM builds b JOIN builds old ON old.build_id = :id WHERE b.build_repo_id = old.build_repo_id AND b.build_event = old.build_event AND b.build_ref = old.build_ref AND b.build_target = old.build_target AND b.build_deploy = old.build_deploy AND b.build_id <> old.build_id`, p)
			if err != nil {
				return err
			}
			var previous int64
			if err = tx.QueryRow(query, args...).Scan(&previous); err != nil {
				return err
			}
			if previous > 0 {
				p["previous"] = previous
				query, args, err = binder.BindNamed("UPDATE latest SET latest_build_id = :previous WHERE latest_build_id = :id", p)
				if err != nil {
					return err
				}
				if _, err = tx.Exec(query, args...); err != nil {
					return err
				}
			}
			for _, sql := range []string{
				"DELETE FROM cards WHERE card_id IN (SELECT step_id FROM steps JOIN stages ON step_stage_id = stage_id WHERE stage_build_id = :id)",
				"DELETE FROM logs WHERE log_id IN (SELECT step_id FROM steps JOIN stages ON step_stage_id = stage_id WHERE stage_build_id = :id)",
				"DELETE FROM steps WHERE step_stage_id IN (SELECT stage_id FROM stages WHERE stage_build_id = :id)",
				"DELETE FROM stages WHERE stage_build_id = :id",
				"DELETE FROM latest WHERE latest_build_id = :id",
				"DELETE FROM builds WHERE build_id = :id",
			} {
				query, args, err = binder.BindNamed(sql, p)
				if err != nil {
					return err
				}
				if _, err = tx.Exec(query, args...); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return out, err
}

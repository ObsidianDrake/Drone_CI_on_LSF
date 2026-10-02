// Package monitor stores sampled workload trends independently of build history.
package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/drone/drone/core"
	"github.com/drone/drone/store/shared/db"
)

const Interval = 30 * time.Second
const Retention = 7 * 24 * time.Hour

type Counts struct {
	BuildRunning, BuildPending, LSFRunning, LSFPending int
	LSFValid                                           bool
}
type Repository struct {
	ID         int64  `json:"id"`
	Slug       string `json:"slug"`
	Created    int64  `json:"-"`
	Visibility string `json:"-"`
}
type Store struct{ DB *db.DB }

// Repositories lists enabled repositories, including those with no current workload.
func (s *Store) Repositories(ctx context.Context) ([]Repository, error) {
	out := []Repository{}
	err := s.DB.View(func(q db.Queryer, b db.Binder) error {
		query, args, err := b.BindNamed("SELECT repo_id, repo_slug, repo_created, repo_visibility FROM repos WHERE repo_active = :active ORDER BY repo_slug", map[string]interface{}{"active": true})
		if err != nil {
			return err
		}
		rows, err := q.Query(query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r Repository
			if err := rows.Scan(&r.ID, &r.Slug, &r.Created, &r.Visibility); err != nil {
				return err
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

type TrackedJob struct {
	Repo int64
	Name string
}
type JobState struct{ Status, Name string }

func (s *Store) Track(id string, repo int64, name string) error {
	return s.DB.Update(func(tx db.Execer, b db.Binder) error {
		p := map[string]interface{}{"id": id, "repo": repo, "name": name}
		for _, sql := range []string{"DELETE FROM trend_jobs WHERE job_id=:id", "INSERT INTO trend_jobs(job_id,job_repo,job_name) VALUES(:id,:repo,:name)"} {
			q, a, e := b.BindNamed(sql, p)
			if e != nil {
				return e
			}
			if _, e = tx.Exec(q, a...); e != nil {
				return e
			}
		}
		return nil
	})
}
func (s *Store) Jobs() (map[string]TrackedJob, error) {
	out := map[string]TrackedJob{}
	err := s.DB.View(func(q db.Queryer, _ db.Binder) error {
		rows, e := q.Query("SELECT job_id,job_repo,job_name FROM trend_jobs")
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var job TrackedJob
			if e = rows.Scan(&id, &job.Repo, &job.Name); e != nil {
				return e
			}
			out[id] = job
		}
		return rows.Err()
	})
	return out, err
}
func (s *Store) Save(at int64, counts map[int64]*Counts, valid bool, done []string) error {
	return s.DB.Update(func(tx db.Execer, b db.Binder) error {
		run := func(sql string, p map[string]interface{}) error {
			q, a, e := b.BindNamed(sql, p)
			if e != nil {
				return e
			}
			_, e = tx.Exec(q, a...)
			return e
		}
		// Read build counts in the same database snapshot as their sample is persisted.
		rows, e := tx.Query("SELECT build_repo_id,build_status,COUNT(*) FROM builds WHERE build_status IN ('running','pending') GROUP BY build_repo_id,build_status")
		if e != nil {
			return e
		}
		for rows.Next() {
			var repo int64
			var status string
			var count int
			if e = rows.Scan(&repo, &status, &count); e != nil {
				rows.Close()
				return e
			}
			c := counts[repo]
			if c == nil {
				c = &Counts{LSFValid: true}
				counts[repo] = c
			}
			if status == core.StatusRunning {
				c.BuildRunning = count
			} else {
				c.BuildPending = count
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		// A duplicate sampling slot is replaced atomically (safe across restarts).
		p := map[string]interface{}{"t": at, "valid": boolInt(valid), "cutoff": at - int64(Retention/time.Second)}
		for _, sql := range []string{"DELETE FROM trend_samples WHERE trend_time=:t", "DELETE FROM trend_frames WHERE trend_time=:t", "INSERT INTO trend_frames(trend_time,trend_lsf_valid) VALUES(:t,:valid)", "DELETE FROM trend_samples WHERE trend_time < :cutoff", "DELETE FROM trend_frames WHERE trend_time < :cutoff"} {
			if e = run(sql, p); e != nil {
				return e
			}
		}
		for repo, c := range counts {
			p := map[string]interface{}{"t": at, "repo": repo, "br": c.BuildRunning, "bp": c.BuildPending, "lr": c.LSFRunning, "lp": c.LSFPending, "valid": boolInt(c.LSFValid)}
			if e = run("INSERT INTO trend_samples(trend_time,trend_repo,build_running,build_pending,lsf_running,lsf_pending,lsf_valid) VALUES(:t,:repo,:br,:bp,:lr,:lp,:valid)", p); e != nil {
				return e
			}
		}
		for _, id := range done {
			if e = run("DELETE FROM trend_jobs WHERE job_id=:id", map[string]interface{}{"id": id}); e != nil {
				return e
			}
		}
		return nil
	})
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Values are RUN and RUN+PEND peaks. Null means missing samples, never zero.
type Series struct {
	Repository
	Values [][4]*int `json:"values"`
}
type Result struct {
	Times         []int64  `json:"times"`
	Series        []Series `json:"series"`
	Step          int64    `json:"step"`
	LastSample    int64    `json:"last_sample"`
	SampleSeconds int      `json:"sample_seconds"`
}

func (s *Store) Read(ctx context.Context, repos []Repository, now time.Time, hours int) (*Result, error) {
	if hours < 1 || hours > 168 || len(repos) > 12 {
		return nil, fmt.Errorf("invalid trend range or repository selection")
	}
	step := int64(30)
	for _, v := range []int64{30, 60, 120, 300, 600, 900} {
		step = v
		if int64(hours*3600)/step <= 720 {
			break
		}
	}
	end := now.Unix() / 30 * 30
	start := (end - int64(hours*3600)) / step * step
	size := int((end-start)/step) + 1
	out := &Result{Step: step, SampleSeconds: 30, Times: make([]int64, size), Series: []Series{}}
	for i := range out.Times {
		out.Times[i] = start + int64(i)*step
	}
	err := s.DB.View(func(q db.Queryer, b db.Binder) error {
		params := map[string]interface{}{"start": start, "end": end}
		bind := func(sql string) (string, []interface{}, error) { return b.BindNamed(sql, params) }
		sql, args, e := bind("SELECT trend_time,trend_lsf_valid FROM trend_frames WHERE trend_time>=:start AND trend_time<=:end ORDER BY trend_time")
		if e != nil {
			return e
		}
		rows, e := q.Query(sql, args...)
		if e != nil {
			return e
		}
		frames := map[int64]bool{}
		for rows.Next() {
			var at int64
			var valid int
			if e = rows.Scan(&at, &valid); e != nil {
				rows.Close()
				return e
			}
			frames[at] = valid == 1
			if at > out.LastSample {
				out.LastSample = at
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, repo := range repos {
			samples := map[int64]Counts{}
			params["repo"] = repo.ID
			sql, args, e = bind("SELECT trend_time,build_running,build_pending,lsf_running,lsf_pending,lsf_valid FROM trend_samples WHERE trend_repo=:repo AND trend_time>=:start AND trend_time<=:end")
			if e != nil {
				return e
			}
			rows, e = q.Query(sql, args...)
			if e != nil {
				return e
			}
			for rows.Next() {
				var at int64
				var valid int
				var c Counts
				if e = rows.Scan(&at, &c.BuildRunning, &c.BuildPending, &c.LSFRunning, &c.LSFPending, &valid); e != nil {
					rows.Close()
					return e
				}
				c.LSFValid = valid == 1
				samples[at] = c
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			series := Series{Repository: repo, Values: make([][4]*int, size)}
			for i, at := range out.Times {
				peaks := [4]int{}
				buildOK, lsfOK := true, true
				for t := at; t < at+step && t <= end; t += 30 {
					valid, exists := frames[t]
					if !exists || t < repo.Created {
						buildOK = false
						lsfOK = false
						continue
					}
					if !valid {
						lsfOK = false
					}
					c, has := samples[t]
					if has && !c.LSFValid {
						lsfOK = false
					}
					values := [4]int{c.BuildRunning, c.BuildRunning + c.BuildPending, c.LSFRunning, c.LSFRunning + c.LSFPending}
					for k, v := range values {
						if v > peaks[k] {
							peaks[k] = v
						}
					}
				}
				for k := range peaks {
					if (k < 2 && buildOK) || (k >= 2 && lsfOK) {
						v := peaks[k]
						series.Values[i][k] = &v
					}
				}
			}
			out.Series = append(out.Series, series)
		}
		return nil
	})
	return out, err
}

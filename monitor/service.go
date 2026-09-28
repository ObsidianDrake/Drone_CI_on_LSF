package monitor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/drone/drone/store/shared/db"
	"github.com/sirupsen/logrus"
)

type Service struct {
	Store          *Store
	Bjobs          string
	Enabled        bool
	CommandTimeout time.Duration
	// Query is injectable for tests. Production always queries explicitly tracked IDs.
	Query   func(context.Context, []string) (map[string]JobState, error)
	mu      sync.Mutex
	pending map[string]TrackedJob
}

var numericID = regexp.MustCompile(`^[1-9][0-9]*$`)

// Track is called after bsub returns its ID; repo=0 confirms terminal completion.
// Failed writes are retried and suppress LSF samples until tracking is complete.
func (s *Service) Track(repo int64, id string, name string) {
	if !numericID.MatchString(id) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		s.pending = map[string]TrackedJob{}
	}
	s.pending[id] = TrackedJob{Repo: repo, Name: name}
	if err := s.persist(id, TrackedJob{Repo: repo, Name: name}); err != nil {
		logrus.WithError(err).Error("trends: cannot persist LSF tracking; will retry")
	} else {
		delete(s.pending, id)
	}
}
func (s *Service) persist(id string, job TrackedJob) error {
	if job.Repo > 0 {
		return s.Store.Track(id, job.Repo, job.Name)
	}
	return s.Store.DB.Lock(func(tx db.Execer, b db.Binder) error {
		q, a, e := b.BindNamed("DELETE FROM trend_jobs WHERE job_id=:id", map[string]interface{}{"id": id})
		if e != nil {
			return e
		}
		_, e = tx.Exec(q, a...)
		return e
	})
}
func (s *Service) flush() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, repo := range s.pending {
		if s.persist(id, repo) == nil {
			delete(s.pending, id)
		}
	}
	return len(s.pending) == 0
}
func (s *Service) Run(ctx context.Context) error {
	sample := func() {
		if err := s.Sample(ctx, time.Now()); err != nil {
			logrus.WithError(err).Error("trends: sampling failed; leaving a data gap")
		}
	}
	sample()
	ticker := time.NewTicker(Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			sample()
		}
	}
}
func (s *Service) Sample(ctx context.Context, at time.Time) error {
	valid := s.flush() && s.Enabled
	counts := map[int64]*Counts{}
	done := []string{}
	jobs, err := s.Store.Jobs()
	if err != nil {
		return err
	}
	if s.Enabled && valid {
		ids := []string{}
		for id, job := range jobs {
			repo := job.Repo
			ids = append(ids, id)
			if counts[repo] == nil {
				counts[repo] = &Counts{LSFValid: true}
			}
		}
		sort.Strings(ids)
		// Bounded concurrency and duration; one bjobs call per 100 Drone jobs.
		ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		defer cancel()
		var mu sync.Mutex
		var wg sync.WaitGroup
		work := make(chan []string)
		query := s.Query
		if query == nil {
			query = s.query
		}
		for worker := 0; worker < 4; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for batch := range work {
					states, err := query(ctx, batch)
					if err != nil {
						logrus.WithError(err).Warn("trends: incomplete LSF sample")
					}
					mu.Lock()
					for _, id := range batch {
						c := counts[jobs[id].Repo]
						state := states[id]
						if state.Name != "" && state.Name != jobs[id].Name {
							c.LSFValid = false
							done = append(done, id)
							continue
						}
						switch state.Status {
						case "RUN":
							c.LSFRunning++
						case "PEND":
							c.LSFPending++
						case "DONE", "EXIT":
							done = append(done, id)
						case "WAIT", "PROV", "PSUSP", "USUSP", "SSUSP": // known, but neither RUN nor PEND
						default:
							c.LSFValid = false
						}
					}
					mu.Unlock()
				}
			}()
		}
		for i := 0; i < len(ids); i += 100 {
			end := i + 100
			if end > len(ids) {
				end = len(ids)
			}
			work <- ids[i:end]
		}
		close(work)
		wg.Wait()
	}
	return s.Store.Save(at.Unix()/30*30, counts, valid, done)
}
func (s *Service) query(ctx context.Context, ids []string) (map[string]JobState, error) {
	states := map[string]JobState{}
	if len(ids) == 0 {
		return states, nil
	}
	for _, id := range ids {
		if !numericID.MatchString(id) {
			return states, fmt.Errorf("invalid tracked job id")
		}
	}
	timeout := s.CommandTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	binary := s.Bjobs
	if binary == "" {
		binary = "bjobs"
	}
	args := append([]string{"-a", "-noheader", "-o", "jobid stat job_name:250"}, ids...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && numericID.MatchString(fields[0]) {
			states[fields[0]] = JobState{Status: fields[1], Name: fields[2]}
		}
	}
	return states, err
}

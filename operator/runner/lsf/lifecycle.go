package lsf

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/drone/drone-runtime/engine"
)

// LifecycleEngine belongs to one runtime invocation. The legacy runtime does
// not join detached log streams or return Destroy errors, so retain both here.
type LifecycleEngine struct {
	engine.Engine
	ctx     context.Context
	mu      sync.Mutex
	streams []<-chan struct{}
	// Read only after runtime.Run returns (including its deferred Destroy).
	CleanupErr      error
	DetachedResults map[*engine.Step]DetachedResult
}

type DetachedResult struct {
	State                      engine.State
	Err                        error
	Confirmed, StoppedByRunner bool
	Finished                   time.Time
}

// WithContext binds Setup cancellation and drains detached logs at destruction.
func WithContext(backend engine.Engine, ctx context.Context) *LifecycleEngine {
	return &LifecycleEngine{Engine: backend, ctx: ctx}
}

func (e *LifecycleEngine) Setup(_ context.Context, spec *engine.Spec) error {
	return e.Engine.Setup(e.ctx, spec)
}

type trackedStream struct {
	io.ReadCloser
	once sync.Once
	done chan struct{}
}

func (r *trackedStream) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() { close(r.done) })
	return err
}

func (e *LifecycleEngine) Tail(ctx context.Context, spec *engine.Spec, step *engine.Step) (io.ReadCloser, error) {
	rc, err := e.Engine.Tail(ctx, spec, step)
	if err != nil || !step.Detach {
		return rc, err
	}
	r := &trackedStream{ReadCloser: rc, done: make(chan struct{})}
	e.mu.Lock()
	e.streams = append(e.streams, r.done)
	e.mu.Unlock()
	return r, nil
}

func (e *LifecycleEngine) Destroy(ctx context.Context, spec *engine.Spec) error {
	jobs := make(map[*engine.Step]*job)
	if backend, ok := e.Engine.(*Engine); ok {
		if p, err := backend.lookup(spec); err == nil {
			p.mu.Lock()
			for step, j := range p.jobs {
				if step.Detach && j.id != "" {
					jobs[step] = j
				}
			}
			p.mu.Unlock()
		}
	}
	// Cancel/kill jobs first; waiting for log EOF before this would deadlock
	// on a long-running service. The open log descriptor survives RemoveAll.
	e.CleanupErr = e.Engine.Destroy(ctx, spec)
	e.DetachedResults = make(map[*engine.Step]DetachedResult)
	for step, j := range jobs {
		select {
		case <-j.done:
			e.DetachedResults[step] = DetachedResult{State: j.state, Err: j.err,
				Confirmed: j.confirmed, StoppedByRunner: j.stoppedByRunner, Finished: j.finished}
		case <-ctx.Done():
			e.CleanupErr = errors.Join(e.CleanupErr, ctx.Err())
			return e.CleanupErr
		}
	}
	e.mu.Lock()
	streams := append([]<-chan struct{}(nil), e.streams...)
	e.mu.Unlock()
	for _, done := range streams {
		select {
		case <-done:
			// runtime.stream closes the reader after GotLogs uploads the log.
		case <-ctx.Done():
			e.CleanupErr = errors.Join(e.CleanupErr, ctx.Err())
			return e.CleanupErr
		}
	}
	return e.CleanupErr
}

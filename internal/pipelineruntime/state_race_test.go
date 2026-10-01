package runtime

import (
	"errors"
	"sync"
	"testing"

	"github.com/drone/drone-runtime/engine"
)

func TestConcurrentErrorSnapshots(t *testing.T) {
	r := New(WithConfig(&engine.Spec{}))
	failure := errors.New("parallel submission failed")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(writer bool) {
			defer wg.Done()
			for n := 0; n < 1000; n++ {
				if writer {
					r.setError(failure)
				} else if got := snapshot(r, nil, nil).Runtime.Error; got != nil && got != failure {
					t.Errorf("invalid error snapshot: %v", got)
				}
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if got := snapshot(r, nil, nil).Runtime.Error; got != failure {
		t.Fatalf("final error: %v", got)
	}
}

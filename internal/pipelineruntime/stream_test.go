package runtime

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/drone/drone-runtime/engine"
)

type failingLogReader struct{ err error }

func (r failingLogReader) Read([]byte) (int, error) { return 0, r.err }

func TestStreamPreservesPartialLogAndReportsErrors(t *testing.T) {
	readErr := errors.New("shared filesystem read failed")
	uploadErr := errors.New("log store unavailable")
	for _, failRead := range []bool{false, true} {
		for _, failUpload := range []bool{false, true} {
			var uploaded string
			s := &State{Step: &engine.Step{Metadata: engine.Metadata{Name: "test"}}, config: &engine.Spec{}, hook: &Hook{}}
			s.hook.GotLogs = func(_ *State, lines []*Line) error {
				for _, line := range lines {
					uploaded += line.Message
				}
				if failUpload {
					return uploadErr
				}
				return nil
			}
			var input io.Reader = strings.NewReader("collected output\n")
			if failRead {
				input = io.MultiReader(input, failingLogReader{readErr})
			}
			err := stream(s, io.NopCloser(input))
			if errors.Is(err, readErr) != failRead || errors.Is(err, uploadErr) != failUpload {
				t.Fatalf("read=%v upload=%v err=%v", failRead, failUpload, err)
			}
			if uploaded != "collected output\n" {
				t.Fatalf("collected log was lost: %q", uploaded)
			}
		}
	}
}

package lsf

import (
	_ "embed"
	"fmt"
)

//go:embed clone.sh
var cloneScript string

func nativeCloneScript(depth int) string {
	if depth < 0 {
		depth = 0
	}
	return fmt.Sprintf("clone_depth=%d\n", depth) + cloneScript
}

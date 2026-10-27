//go:build js

package updater

import (
	"fmt"
	"os/exec"
)

// applyDetachAttrs is a no-op on js/wasm: the WebAssembly environment has
// no process tree to detach a child from (startHelper refuses to run).
func applyDetachAttrs(cmd *exec.Cmd) {
}

func wrapHelperSpawnError(err error) error {
	return fmt.Errorf("updater: spawn helper: %w", err)
}

func startHelper(cmd *exec.Cmd) error {
	return fmt.Errorf("updater: cannot spawn a helper process on js/wasm")
}

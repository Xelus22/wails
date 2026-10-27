//go:build js

package fileexplorer

import (
	"errors"
	"syscall"
)

func explorerBinArgs(path string, selectFile bool) (string, []string, error) {
	return "", nil, errors.New("no file explorer is available on js/wasm")
}

func sysProcAttr(path string, selectFile bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

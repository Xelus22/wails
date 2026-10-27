//go:build js

package operatingsystem

import (
	"fmt"
	"runtime"
)

func platformInfo() (*OS, error) {
	return &OS{
		ID:       "js",
		Name:     fmt.Sprintf("WebAssembly (%s)", runtime.Version()),
		Version:  fmt.Sprintf("Go %s", runtime.Version()),
		Branding: "Wails Web",
	}, nil
}

package webwrapper

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestWailsRuntimeMatchesDesktopBundle asserts that the shipped
// `wails-runtime.js` is a byte-exact copy of the desktop runtime bundle.
//
// The web build cannot import the tagged `internal/frontend/runtime` embeds
// (the CLI's plain build sees the debug/!production variant), so the copy is
// canonical for web pages. If the desktop runtime bundle is regenerated, this
// test fails until `wails-runtime.js` is refreshed — the same contract CI
// enforces for `internal/assetserver/bundledassets/runtime.js`.
func TestWailsRuntimeMatchesDesktopBundle(t *testing.T) {
	shipped, err := os.ReadFile(filepath.Join("wails-runtime.js"))
	if err != nil {
		t.Fatalf("read shipped wails-runtime.js: %v", err)
	}
	desktop, err := os.ReadFile(filepath.Join("..", "runtime_prod_desktop.js"))
	if err != nil {
		t.Fatalf("read desktop runtime_prod_desktop.js: %v", err)
	}
	if !bytes.Equal(shipped, desktop) {
		t.Fatalf("wails-runtime.js has drifted from runtime_prod_desktop.js — re-copy the desktop runtime bundle into the webwrapper package")
	}
}

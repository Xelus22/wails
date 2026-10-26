package build

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wailsapp/wails/v2/internal/project"
)

func TestIsWebPlatform(t *testing.T) {
	for _, alias := range []string{"web", "WEB", "js/wasm", "js/WASM", "wasm", " wasm "} {
		if !IsWebPlatform(alias) {
			t.Errorf("IsWebPlatform(%q) = false, want true", alias)
		}
	}
	for _, other := range []string{"linux", "darwin/arm64", "windows", "", "webapp2"} {
		if IsWebPlatform(other) {
			t.Errorf("IsWebPlatform(%q) = true, want false", other)
		}
	}
}

func TestWebCompileTags(t *testing.T) {
	tags, warnings := webCompileTags([]string{"myfeature", "desktop", "production", "debug", "dev", "obfuscated", "webbridge"})
	expected := []string{"myfeature"}
	if len(tags) != len(expected) {
		t.Fatalf("tags = %v, want %v", tags, expected)
	}
	for i, tag := range expected {
		if tags[i] != tag {
			t.Errorf("tags[%d] = %q, want %q", i, tags[i], tag)
		}
	}
	// Every dropped tag must produce a warning; webbridge is dropped silently.
	if len(warnings) != 5 {
		t.Errorf("warnings = %v, want 5 warnings (desktop/production/debug/dev/obfuscated)", warnings)
	}
}

func TestWebCompileTagsKeepsArbitraryTags(t *testing.T) {
	tags, warnings := webCompileTags([]string{"webkitten", "dev2"})
	if len(tags) != 2 || tags[0] != "webkitten" || tags[1] != "dev2" {
		t.Errorf("tags = %v, want [webkitten dev2]", tags)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

func testProjectWithFrontendDist(t *testing.T) (*project.Project, string) {
	t.Helper()
	root := t.TempDir()
	frontendDist := filepath.Join(root, "frontend", "dist")
	if err := os.MkdirAll(frontendDist, 0o755); err != nil {
		t.Fatal(err)
	}
	return &project.Project{Path: root, FrontendDir: filepath.Join(root, "frontend")}, frontendDist
}

func TestLocateWebDistPrefersFrontendDist(t *testing.T) {
	projectData, want := testProjectWithFrontendDist(t)
	got, err := locateWebDist(projectData)
	if err != nil {
		t.Fatalf("locateWebDist: %v", err)
	}
	if got != want {
		t.Errorf("locateWebDist = %q, want %q", got, want)
	}
}

func TestLocateWebDistErrorsWithoutAnyCandidate(t *testing.T) {
	root := t.TempDir()
	projectData := &project.Project{Path: root, FrontendDir: filepath.Join(root, "frontend")}
	_, err := locateWebDist(projectData)
	if err == nil {
		t.Fatal("locateWebDist expected an error when no dist directory exists")
	}
}

func TestVerifyWebDistMarkers(t *testing.T) {
	dist := t.TempDir()
	wasmDir := filepath.Join(dist, "wasm")
	if err := os.MkdirAll(wasmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dist, "assets", "index.js")
	if err := os.MkdirAll(filepath.Dir(bundle), 0o755); err != nil {
		t.Fatal(err)
	}

	// No wasm object -> hard error.
	if _, err := verifyWebDist(dist); err == nil {
		t.Fatal("verifyWebDist expected an error when wasm/app.wasm is missing")
	}
	if err := os.WriteFile(filepath.Join(wasmDir, "app.wasm"), []byte("not wasm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyWebDist(dist); err == nil {
		t.Fatal("verifyWebDist expected an error for a non-wasm object")
	}
	if err := os.WriteFile(filepath.Join(wasmDir, "app.wasm"), []byte("\x00asm\x01\x00\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No bootstrap references -> two warnings (bridge + runtime).
	warnings, err := verifyWebDist(dist)
	if err != nil {
		t.Fatalf("verifyWebDist: %v", err)
	}
	if len(warnings) != 2 {
		t.Errorf("warnings = %v, want 2 (missing bridge + runtime)", warnings)
	}

	// Bootstrap bundled into the dist JS -> no warnings.
	content := "// bridge stub\n" + bridgeMarker + "\nwindow.WailsInvoke(" + "'runtime:ready'" + ");\n"
	if err := os.WriteFile(bundle, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	warnings, err = verifyWebDist(dist)
	if err != nil {
		t.Fatalf("verifyWebDist: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none after bundling the bootstrap", warnings)
	}
}

func TestScanWebDistMarkersSkipsBinaries(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "app.wasm"), []byte(bridgeMarker+"\x00asm"), 0o644); err != nil {
		t.Fatal(err)
	}
	bridgeFound, runtimeFound := scanWebDistMarkers(dist)
	if bridgeFound || runtimeFound {
		t.Errorf("markers must not be matched inside *.wasm binaries (bridge=%t runtime=%t)", bridgeFound, runtimeFound)
	}
}

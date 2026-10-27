package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsWebPlatform(t *testing.T) {
	for _, alias := range []string{"web", "WEB", "js/wasm", "js/WASM", "wasm", " wasm "} {
		assert.True(t, IsWebPlatform(alias), "IsWebPlatform(%q) should be true", alias)
	}
	for _, other := range []string{"linux", "darwin/arm64", "windows", "", "webapp2", "linux,web"} {
		assert.False(t, IsWebPlatform(other), "IsWebPlatform(%q) should be false", other)
	}
}

func TestValidateWebPlatform(t *testing.T) {
	assert.NoError(t, validateWebPlatform("", "wails3 build"))
	assert.NoError(t, validateWebPlatform("web", "wails3 build"))
	assert.NoError(t, validateWebPlatform("js/wasm", "wails3 dev"))
	assert.NoError(t, validateWebPlatform("wasm", "wails3 dev"))

	err := validateWebPlatform("linux", "wails3 build")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "linux")
	assert.Contains(t, err.Error(), "wails3 build")
}

func TestWebCompileTags(t *testing.T) {
	tags, warnings := webCompileTags("webkitten, obfuscated ,dev2,obfuscated,")
	assert.Equal(t, []string{"webkitten", "dev2"}, tags)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "obfuscated")

	tags, warnings = webCompileTags("wails,jsbridge")
	assert.Equal(t, []string{"wails", "jsbridge"}, tags)
	assert.Empty(t, warnings)

	// The webbridge tag (v2) is not required on v3 and must pass through.
	tags, _ = webCompileTags("webbridge,custom")
	assert.Equal(t, []string{"webbridge", "custom"}, tags)

	tags, warnings = webCompileTags("")
	assert.Empty(t, tags)
	assert.Empty(t, warnings)
}

func TestLocateWebDist(t *testing.T) {
	root := t.TempDir()
	frontendDist := filepath.Join(root, "frontend", "dist")
	require.NoError(t, os.MkdirAll(frontendDist, 0o755))

	// A bundler dist wins over the plain frontend dir.
	got, err := locateWebDist(root)
	require.NoError(t, err)
	assert.Equal(t, frontendDist, got)

	// Without a dist, the frontend dir itself is the dist.
	require.NoError(t, os.RemoveAll(frontendDist))
	got, err = locateWebDist(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "frontend"), got)

	// No frontend at all: actionable error.
	require.NoError(t, os.RemoveAll(filepath.Join(root, "frontend")))
	_, err = locateWebDist(root)
	require.Error(t, err)
}

func TestWebBindingsConventionTS(t *testing.T) {
	root := t.TempDir()

	// No bindings tree: plain-JS convention.
	assert.False(t, webBindingsConventionTS(root))

	// A TS bindings tree switches the convention.
	tsDir := filepath.Join(root, "frontend", "bindings", "main")
	require.NoError(t, os.MkdirAll(tsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(tsDir, "index.ts"), []byte("export {}"), 0o644))
	assert.True(t, webBindingsConventionTS(root))
}

func TestWebTaskVars(t *testing.T) {
	t.Setenv("EXTRA_TAGS", "")
	vars := webTaskVars(&WebBuildOptions{Tags: " feature "})
	assert.Equal(t, []string{"EXTRA_TAGS=feature"}, vars)

	vars = webTaskVars(&WebBuildOptions{Dev: true, Tags: "a,b"})
	assert.ElementsMatch(t, []string{"EXTRA_TAGS=a,b", "DEV=true"}, vars)

	vars = webTaskVars(&WebBuildOptions{})
	assert.Empty(t, vars)
}

func TestScanWebDistMarkers(t *testing.T) {
	dist := t.TempDir()
	bundled := filepath.Join(dist, "assets", "index.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(bundled), 0o755))

	// Neither marker: both false.
	glueFound, runtimeFound := scanWebDistMarkers(dist)
	assert.False(t, glueFound)
	assert.False(t, runtimeFound)

	content := "// glue literal\n" + webGlueMarker + "loading...\n" +
		"console.debug(\"" + webRuntimeMarker + "\", 42);\n"
	require.NoError(t, os.WriteFile(bundled, []byte(content), 0o644))

	// Markers in plain text files are found.
	glueFound, runtimeFound = scanWebDistMarkers(dist)
	assert.True(t, glueFound)
	assert.True(t, runtimeFound)

	// Binary (*.wasm) files and vendored dirs are excluded: a fake marker
	// inside them must not be reported.
	require.NoError(t, os.MkdirAll(filepath.Join(dist, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "node_modules", "vendor.js"), []byte(webGlueMarker), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dist, "wasm"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist, "wasm", "app.wasm"), []byte(webGlueMarker+"\x00asm"), 0o644))
	glueFound, _ = scanWebDistMarkers(dist)
	assert.True(t, glueFound) // still from assets/index.js only
	// A negative control: a dist which only contains excluded paths.
	dist2 := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dist2, "node_modules"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dist2, "node_modules", "vendor.js"), []byte(webGlueMarker+webRuntimeMarker), 0o644))
	glueFound, runtimeFound = scanWebDistMarkers(dist2)
	assert.False(t, glueFound)
	assert.False(t, runtimeFound)
}

func TestVerifyWebDist(t *testing.T) {
	dist := t.TempDir()
	wasmDir := filepath.Join(dist, "wasm")
	require.NoError(t, os.MkdirAll(wasmDir, 0o755))

	// Missing object: hard error.
	_, err := verifyWebDist(dist)
	require.Error(t, err)

	// Corrupt object: hard error.
	require.NoError(t, os.WriteFile(filepath.Join(wasmDir, "app.wasm"), []byte("not wasm"), 0o644))
	_, err = verifyWebDist(dist)
	require.Error(t, err)

	// Valid object without markers: two warnings.
	require.NoError(t, os.WriteFile(filepath.Join(wasmDir, "app.wasm"), []byte("\x00asm\x01\x00\x00\x00"), 0o644))
	warnings, err := verifyWebDist(dist)
	require.NoError(t, err)
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "wasm-glue.js")
	assert.Contains(t, warnings[1], "@wailsio/runtime")
}

func TestEnvHelpers(t *testing.T) {
	env := []string{"GOOS=linux", "PATH=/usr/bin", "CGO_ENABLED=1"}

	env = envSet(env, "GOOS", "js")
	env = envSet(env, "GOOS", "js") // idempotent
	assert.Contains(t, env, "GOOS=js")
	assert.NotContains(t, env, "GOOS=linux")

	env = envUnset(env, "CGO_ENABLED")
	assert.NotContains(t, env, "CGO_ENABLED=1")
	assert.Contains(t, env, "PATH=/usr/bin")

	// Removing an absent key leaves the slice untouched.
	envBefore := append([]string{}, env...)
	env = envUnset(env, "NOT_THERE")
	assert.Equal(t, envBefore, env)
}

func TestExtractWebBootstrap(t *testing.T) {
	target := t.TempDir()
	require.NoError(t, extractWebBootstrap(target))

	// All three bootstrap files are written.
	for _, name := range []string{"wasm-glue.js", "WASM_README.txt", "go-exec.js"} {
		info, err := os.Stat(filepath.Join(target, name))
		require.NoError(t, err, name)
		assert.Greater(t, info.Size(), int64(0), name)
	}

	// go-exec.js is the toolchain's wasm_exec.js.
	goroot, err := goEnviron("GOROOT")
	require.NoError(t, err)
	execSource, err := os.ReadFile(filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"))
	require.NoError(t, err)
	shipped, err := os.ReadFile(filepath.Join(target, "go-exec.js"))
	require.NoError(t, err)
	assert.Equal(t, execSource, shipped)

	// The glue carries the dist-scan marker.
	glue, err := os.ReadFile(filepath.Join(target, "wasm-glue.js"))
	require.NoError(t, err)
	assert.Contains(t, string(glue), webGlueMarker)
	assert.Contains(t, string(glue), "activateWasmBridge")
}

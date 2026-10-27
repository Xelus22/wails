package commands

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/internal/browser"
	"github.com/wailsapp/wails/v3/internal/flags"
	"github.com/wailsapp/wails/v3/internal/term"
)

// canonicalWebPlatform is the internal name for the web (WASM) target.
const canonicalWebPlatform = "web"

// webDevServerAddress is the static address the web dev server binds to.
// It matches the asset-server base URL stub for js/wasm builds
// (internal/assetserver assetserver_js.go).
const webDevServerAddress = "localhost:34115"

// webGlueMarker is a console literal unique to the shipped wasm-glue.js.
// Minifiers keep string contents, so the marker survives a bundled build.
// It detects that the frontend entry point actually imports the bootstrap.
const webGlueMarker = "[wails] WASM bootstrap "

// webRuntimeMarker is a literal unique to the Wails runtime's wasm bridge
// (deliverResult debug aid in internal/runtime runtime wasm.ts). Its
// presence in the dist means the runtime bundle itself was bundled in.
const webRuntimeMarker = "wasm bridge: reply for unknown call"

// IsWebPlatform reports whether the platform value selects the web (WASM)
// flavor. Accepted aliases: `web`, `js/wasm` (the Go platform name) and
// `wasm`.
func IsWebPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "web", "js/wasm", "wasm":
		return true
	}
	return false
}

// validateWebPlatform rejects unsupported -platform values for verbs the
// web pipeline covers; an empty value selects the verb's default flow.
func validateWebPlatform(platform, verb string) error {
	if platform == "" {
		return nil
	}
	if !IsWebPlatform(platform) {
		return fmt.Errorf("platform '%s' is not supported by `%s` — use '-platform web' (aliases: js/wasm, wasm) for the WebAssembly target; native targets use GOOS=/ARCH= task variables", platform, verb)
	}
	return nil
}

// WebBuildOptions carries the flags the web (WASM) build consumes.
type WebBuildOptions struct {
	// Tags are additional Go build tags (comma-separated). Desktop-only
	// tags such as `obfuscated` are dropped with a warning; the js/wasm
	// build needs no extra pipeline tag.
	Tags string
	// Obfuscated / GarbleArgs apply to desktop builds only; the web build
	// warns and ignores them.
	Obfuscated bool
	GarbleArgs string
	// Dev compiles the Go module with -gcflags 'all=-N -l' for debugging.
	Dev bool
	// Silent skips all console output (used by tests).
	Silent bool
}

// WebBuildResult describes the artifacts of a web (WASM) build.
type WebBuildResult struct {
	// DistDirectory is the directory a browser should be served from
	// (contains wasm/app.wasm).
	DistDirectory string
	// WasmPath is the compiled backend module (glue candidate #2).
	WasmPath string
	// GlueDirectory is where the bootstrap glue was extracted
	// (<frontend>/bindings/wasm).
	GlueDirectory string
}

// WebBuild produces the web (WASM) flavor of a Wails v3 application:
//
//  1. the project Taskfile's `common:build:frontend` task runs the
//     conventional frontend build (frontend deps, native bindings,
//     bundler) when a Taskfile exists; without one the frontend build is
//     skipped with a warning and the existing frontend output is used;
//  2. a wasm-aware bindings pass analyses the js/wasm graph and emits
//     the markup-free bindings catalogue (wasm.catalog.json);
//  3. the browser bootstrap (wasm-glue.js, go-exec.js, WASM_README.txt)
//     is extracted under <frontend>/bindings/wasm;
//  4. the Go backend is compiled with GOOS=js GOARCH=wasm into
//     <dist>/wasm/app.wasm;
//  5. the dist bundle is sanity-checked and non-fatal warnings are
//     reported for a dist that will not boot the bridge.
func WebBuild(options *WebBuildOptions) (*WebBuildResult, error) {
	result := &WebBuildResult{}

	projectDir, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if !options.Silent {
		if !underWake() {
			term.Header("Build (Web/WASM)")
		}
		warnIgnoredWebFlags(options)
	}

	// Build the frontend through the project's own pipeline ------------- -
	// `common:build:frontend` honours the template conventions: frontend
	// dependencies, native bindings generation and the bundler.
	taskVars := webTaskVars(options)
	if hasProjectTaskfile() {
		if err := runTaskFunc(&RunTaskOptions{Name: "common:build:frontend"}, taskVars); err != nil {
			return nil, err
		}
	} else {
		if !options.Silent {
			term.Warningf("no Taskfile found in %s — skipping the frontend build; using the existing frontend output as-is", projectDir)
		}
	}

	// Wasm-aware bindings pass -----------------------------------------
	// Emits/updates the bindings and wasm.catalog.json. It reports
	// differences between the native and the js/wasm binding surfaces.
	if err := GenerateBindings(&flags.GenerateBindingsOptions{
		OutputDirectory: filepath.Join(projectDir, "frontend", "bindings"),
		ModelsFilename:  "models",
		IndexFilename:   "index",
		TimeType:        "Date",
		Clean:           true,
		TS:              webBindingsConventionTS(projectDir),
		Wasm:            true,
		WasmCatalog:     true,
	}, nil); err != nil {
		return nil, err
	}

	// Extract the browser bootstrap AFTER the bindings passes (a -clean
	// regeneration wipes the directory otherwise).
	glueDir := filepath.Join(projectDir, "frontend", "bindings", "wasm")
	if err := extractWebBootstrap(glueDir); err != nil {
		return nil, err
	}
	result.GlueDirectory = glueDir

	// Compile the backend module ----------------------------------------
	dist, err := locateWebDist(projectDir)
	if err != nil {
		return nil, err
	}
	result.DistDirectory = dist

	wasmPath, err := compileWasmObject(projectDir, dist, options)
	if err != nil {
		return nil, err
	}
	result.WasmPath = wasmPath

	// Sanity-check the dist output --------------------------------------
	warnings, err := verifyWebDist(dist)
	if err != nil {
		return nil, err
	}
	if !options.Silent {
		for _, warning := range warnings {
			term.Warning(warning)
		}
		if stats, err := os.Stat(wasmPath); err == nil {
			term.Infof("WASM backend: %s (%.1f MB)", wasmPath, float64(stats.Size())/(1024*1024))
		}
		term.Infof("Web app built in: %s", dist)
		term.Success("Web build completed")
	}
	return result, nil
}

// webDevApplication builds the project as a wasm module and serves the
// dist directory over plain HTTP. v1 of the web dev flow has no watcher:
// changes require re-running the command and refreshing the browser.
func webDevApplication(options *DevOptions) error {
	if !underWake() {
		term.Header("Dev (Web/WASM)")
	}
	term.Warning("wails3 dev -platform web does not reload automatically yet — re-run the command (or `wails3 build -platform web`) and refresh the browser to pick up changes.")

	result, err := WebBuild(&WebBuildOptions{Dev: true})
	if err != nil {
		return err
	}

	if options.Browser {
		go func() {
			time.Sleep(500 * time.Millisecond)
			if err := browser.OpenURL("http://" + webDevServerAddress); err != nil {
				term.Warningf("cannot open the browser: %s", err.Error())
			}
		}()
	}

	term.Infof("Serving %s at http://%s (Ctrl+C to stop)", result.DistDirectory, webDevServerAddress)
	return ServeWebDist(result.DistDirectory, webDevServerAddress)
}

// ServeWebDist serves the built web application from a dist directory over
// plain HTTP (the handler pins the .wasm content type regardless of local
// mime.types databases). It blocks until the listener fails.
func ServeWebDist(dist, address string) error {
	if !dirExists(dist) {
		return fmt.Errorf("cannot serve the web app: directory '%s' does not exist", dist)
	}
	fileServer := http.FileServer(http.Dir(dist))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.ToLower(r.URL.Path), ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		fileServer.ServeHTTP(w, r)
	})
	return http.ListenAndServe(address, handler)
}

// warnIgnoredWebFlags reports desktop-only flags that never apply to the
// web target so shared flag sets do not silently change behaviour.
func warnIgnoredWebFlags(options *WebBuildOptions) {
	if options.Obfuscated {
		term.Warning("obfuscated is not supported for the js/wasm target — ignoring")
	}
	if options.GarbleArgs != "" {
		term.Warning("garble is not supported for the js/wasm target — ignoring garbleargs")
	}
}

// webTaskVars assembles the Taskfile CLI variables the frontend task
// receives, mirroring the desktop wrapTask wiring.
func webTaskVars(options *WebBuildOptions) []string {
	var vars []string
	tags := strings.TrimSpace(mergeTags(options.Tags, envTags()...))
	if tags != "" {
		vars = append(vars, "EXTRA_TAGS="+tags)
	}
	if options.Dev {
		vars = append(vars, "DEV=true")
	}
	return vars
}

// hasProjectTaskfile reports whether the current directory holds a root
// Taskfile (the pipeline drives the project's own build pipeline when
// one exists).
func hasProjectTaskfile() bool {
	for _, name := range []string{"Taskfile.yml", "Taskfile.yaml"} {
		if fileExists(name) {
			return true
		}
	}
	return false
}

// webBindingsConventionTS detects the bindings convention: an existing
// TypeScript bindings tree (frontend/bindings/**/index.ts) means the
// project generates TS bindings, so the wasm pass keeps that convention.
func webBindingsConventionTS(projectDir string) bool {
	root := filepath.Join(projectDir, "frontend", "bindings")
	if !dirExists(root) {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipAll
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "index.ts" {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// locateWebDist resolves the directory the pipeline compiles the module
// into and serves: the frontend dist directory when a bundler produced
// one, else the frontend directory itself.
func locateWebDist(projectDir string) (string, error) {
	frontendDist := filepath.Join(projectDir, "frontend", "dist")
	if dirExists(frontendDist) {
		return frontendDist, nil
	}
	frontend := filepath.Join(projectDir, "frontend")
	if dirExists(frontend) {
		return frontend, nil
	}
	return "", fmt.Errorf("cannot locate a frontend output directory: neither '%s' nor '%s' exists", frontendDist, frontend)
}

// compileWasmObject builds the Go backend for the browser with plain
// js/wasm flags and writes the object to <dist>/wasm/app.wasm.
func compileWasmObject(projectDir, dist string, options *WebBuildOptions) (string, error) {
	wasmDir := filepath.Join(dist, "wasm")
	if err := os.MkdirAll(wasmDir, 0o755); err != nil {
		return "", err
	}
	wasmPath := filepath.Join(wasmDir, "app.wasm")

	env := os.Environ()
	env = envSet(env, "GOOS", "js")
	env = envSet(env, "GOARCH", "wasm")
	env = envSet(env, "CGO_ENABLED", "0")
	env = envUnset(env, "CC")

	tags, tagWarnings := webCompileTags(options.Tags)
	for _, warning := range tagWarnings {
		if !options.Silent {
			term.Warning(warning)
		}
	}

	args := []string{"build", "-buildvcs=false"}
	if options.Dev {
		args = append(args, "-gcflags", "all=-N -l")
	}
	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}
	args = append(args, "-o", wasmPath, ".")

	cmd := exec.Command("go", args...)
	cmd.Dir = projectDir
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("the WASM backend compile failed:\n%s", string(output))
	}
	return wasmPath, nil
}

// extractWebBootstrap writes the browser bootstrap into the target
// directory: the embedded wasm-glue.js and WASM_README.txt, plus the
// compiler toolchain's lib/wasm/wasm_exec.js as go-exec.js. Idempotent.
func extractWebBootstrap(targetDir string) error {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}

	for _, name := range []string{"wasm-glue.js", "WASM_README.txt"} {
		data, err := webAssets.ReadFile(filepath.Join("webassets", name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(targetDir, name), data, 0o644); err != nil {
			return err
		}
	}

	goroot, err := goEnviron("GOROOT")
	if err != nil {
		return err
	}
	wasmExecSource, err := os.ReadFile(filepath.Join(goroot, "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		return fmt.Errorf("cannot read the js/wasm toolchain loader: %w — is the js/wasm support installed for this Go version?", err)
	}
	return os.WriteFile(filepath.Join(targetDir, "go-exec.js"), wasmExecSource, 0o644)
}

// goEnviron runs `go env <name>` against the host toolchain.
func goEnviron(name string) (string, error) {
	output, err := exec.Command("go", "env", name).Output()
	if err != nil {
		return "", fmt.Errorf("`go env %s` failed: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// webCompileTags filters the user's comma-separated build tags for the
// js/wasm compile: desktop-only tags are dropped with a warning.
func webCompileTags(buildFlags string) (tags []string, warnings []string) {
	droppedObfuscated := false
	seen := map[string]bool{}
	for _, tag := range strings.Split(buildFlags, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if tag == "obfuscated" {
			droppedObfuscated = true
			continue
		}
		if !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	if droppedObfuscated {
		warnings = append(warnings, "dropping build tag 'obfuscated' (not applicable to the web/wasm target)")
	}
	return tags, warnings
}

// verifyWebDist sanity-checks the built web app and returns non-fatal
// warnings. It guarantees the wasm object is valid and that the dist
// bundle references the bootstrap (either bundled by a bundler or
// plain-served, as with the fixture layout).
func verifyWebDist(dist string) ([]string, error) {
	var warnings []string

	wasmPath := filepath.Join(dist, "wasm", "app.wasm")
	data, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("the compiled WASM backend is missing: %w", err)
	}
	if len(data) < 8 || string(data[0:4]) != "\x00asm" {
		return nil, fmt.Errorf("%s is not a WebAssembly binary", wasmPath)
	}

	glueFound, runtimeFound := scanWebDistMarkers(dist)
	if !glueFound {
		warnings = append(warnings, "the dist output does not reference the WASM bootstrap — import it at the top of your frontend entry point: `import './bindings/wasm/wasm-glue.js'` (import it BEFORE anything that touches the Wails runtime) and rebuild")
	}
	if !runtimeFound {
		warnings = append(warnings, "the dist output does not reference the Wails runtime's wasm bridge — bundle @wailsio/runtime from the npm package so runtime calls reach the bridge")
	}
	return warnings, nil
}

// scanWebDistMarkers walks the dist directory (text files only) and
// reports whether the wasm-glue and the runtime bridge markers are
// present.
func scanWebDistMarkers(dist string) (glueFound bool, runtimeFound bool) {
	skipDirs := map[string]bool{"node_modules": true, ".git": true}
	_ = filepath.WalkDir(dist, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries never fail the build
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".wasm") {
			return nil // our own binary output, always marker-free
		}
		data, err2 := os.ReadFile(path)
		if err2 != nil {
			return nil
		}
		// HTML filters (minifiers) may escape some bytes; the markers are
		// plain ASCII so a raw substring check is sufficient.
		if bytes.Contains(data, []byte(webGlueMarker)) {
			glueFound = true
		}
		if bytes.Contains(data, []byte(webRuntimeMarker)) {
			runtimeFound = true
		}
		return nil
	})
	return glueFound, runtimeFound
}

// --- small filesystem/env helpers (v3 has no internal/shell package) ----

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func envSet(env []string, key, value string) []string {
	env = envUnset(env, key)
	return append(env, key+"="+value)
}

func envUnset(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func splitAndTrimTags(buildFlags string) string {
	var parts []string
	for _, tag := range strings.Split(buildFlags, ",") {
		if tag = strings.TrimSpace(tag); tag != "" {
			parts = append(parts, tag)
		}
	}
	return strings.Join(parts, ",")
}

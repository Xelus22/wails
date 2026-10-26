package build

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/leaanthony/slicer"
	"github.com/pterm/pterm"
	"github.com/samber/lo"

	"github.com/wailsapp/wails/v2/internal/fs"
	"github.com/wailsapp/wails/v2/internal/project"
	"github.com/wailsapp/wails/v2/internal/shell"
	"github.com/wailsapp/wails/v2/internal/staticanalysis"
	"github.com/wailsapp/wails/v2/pkg/commands/buildtags"
	"github.com/wailsapp/wails/v2/pkg/commands/webbindings"
)

// canonicalWebPlatform is the internal name for the web (WASM) target.
const canonicalWebPlatform = "web"

// webDroppedTags are desktop-only build tags that never apply to the
// js/wasm compile.
var webDroppedTags = []string{"desktop", "production", "debug", "dev", "obfuscated"}

// bridgeMarker is a console literal unique to the shipped wasm-bridge.js.
// Minifiers keep string contents, so the marker survives a bundled build.
// It is used to detect that the frontend entry point actually imports the
// bootstrap.
const bridgeMarker = "[wails] WASM backend has stopped"

// runtimeMarker is a literal unique to the desktop runtime bundle shipped as
// wailsjs/wasm/wails-runtime.js (its final "runtime:ready" invocation).
const runtimeMarker = "runtime:ready"

// IsWebPlatform reports whether the platform target selects the web (WASM)
// flavor. Accepted aliases: `web`, `js/wasm` (the Go platform name) and
// `wasm`.
func IsWebPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "web", "js/wasm", "wasm":
		return true
	}
	return false
}

// WebBuildResult describes the artifacts of a web (WASM) build.
type WebBuildResult struct {
	// DistDirectory is the directory a browser should be served from
	// (frontend dist, containing wasm/app.wasm).
	DistDirectory string
	// WasmPath is the compiled backend module (bridge candidate #2).
	WasmPath string
}

// WebBuild produces the web (WASM) flavor of the application: the frontend is
// built into the dist directory, the backend is compiled as a js/wasm
// webbridge module into <dist>/wasm/app.wasm, and the bindings catalog of the
// real wasm object is probed and verified against the desktop-generated
// wailsjs bindings. Desktop packaging steps (bin dir, installers, asset
// bundles) do not apply.
func WebBuild(options *Options) (*WebBuildResult, error) {
	result := &WebBuildResult{}

	projectData := options.ProjectData
	if projectData == nil {
		return nil, fmt.Errorf("web build: missing project data")
	}
	if options.Logger == nil {
		return nil, fmt.Errorf("web build: missing logger")
	}
	compiler := lo.CoalesceOrEmpty(options.Compiler, "go")
	verbose := options.Verbosity == VERBOSE

	// Flags that make no sense for the web target — warn instead of fail so
	// shared flag sets do not break multi-target builds.
	if options.SkipBindings {
		pterm.Warning.Println("skipbindings is ignored for the web target — the bindings catalog is required at runtime")
	}
	if options.RaceDetector {
		pterm.Warning.Println("race is not supported for the js/wasm target — ignoring")
	}
	if options.Obfuscated {
		pterm.Warning.Println("obfuscated is not supported for the web target — ignoring")
	}

	// Build the frontend first: a vite build may wipe the dist directory, and
	// the wasm artifact is written into it afterwards. The bootstrap is
	// installed before the frontend build so bundlers can resolve
	// `wailsjs/wasm/*` imports (idempotent; the probe refreshes go-exec.js
	// afterwards to match the actual compiler).
	if err := webbindings.InstallWebBootstrap(projectData.Path, options.Compiler); err != nil {
		return nil, fmt.Errorf("cannot install the WASM bootstrap: %w", err)
	}
	if !options.IgnoreFrontend {
		builder := NewBaseBuilder(options)
		builder.SetProjectData(projectData)
		if err := builder.BuildFrontend(options.Logger); err != nil {
			return nil, err
		}
	} else {
		printBulletPoint("Skipping frontend build (-s). ")
	}

	dist, err := locateWebDist(projectData)
	if err != nil {
		return nil, err
	}
	result.DistDirectory = dist

	wasmPath := filepath.Join(dist, "wasm", "app.wasm")
	if err := fs.MkDirs(filepath.Dir(wasmPath)); err != nil {
		return nil, err
	}

	// Compile the backend as a js/wasm webbridge module ----------------------
	compileTags, tagWarnings := webCompileTags(options.UserTags)
	for _, warning := range tagWarnings {
		pterm.Warning.Println(warning)
	}

	envCompile := os.Environ()
	envCompile = shell.SetEnv(envCompile, "GOOS", "js")
	envCompile = shell.SetEnv(envCompile, "GOARCH", "wasm")
	envCompile = shell.SetEnv(envCompile, "CGO_ENABLED", "0")
	envCompile = shell.RemoveEnv(envCompile, "CC")

	if !options.SkipModTidy {
		printBulletPoint("Running go mod tidy: ")
		if verbose {
			pterm.Println("")
		}
		tidyStdout, tidyStderr, err := shell.RunCommand(projectData.Path, compiler, "mod", "tidy")
		if err != nil {
			return nil, fmt.Errorf("go mod tidy failed:\n%s\n%s", tidyStdout, tidyStderr)
		}
	}

	printBulletPoint("Compiling WASM backend: ")
	if verbose {
		pterm.Println("")
	}

	commands := slicer.String()
	commands.Add("build")
	commands.Add("-buildvcs=false")

	// Better debugging in dev/debug mode, mirroring the desktop compile.
	if options.Mode == Dev || options.Mode == Debug {
		commands.Add("-gcflags")
		commands.Add("all=-N -l")
	}

	if options.ForceBuild {
		commands.Add("-a")
	}

	if options.TrimPath {
		commands.Add("-trim_path")
	}

	commands.Add("-tags")
	commands.Add(buildtags.Stringify(append(compileTags, "webbridge")))

	ldflags := slicer.String()
	if options.LDFlags != "" {
		ldflags.Add(options.LDFlags)
	}
	if options.Mode == Production {
		ldflags.Add("-w", "-s")
	}
	ldflags.Deduplicate()
	if ldflags.Length() > 0 {
		commands.Add("-ldflags")
		commands.Add(ldflags.Join(" "))
	}

	commands.Add("-o")
	commands.Add(wasmPath)

	if verbose {
		pterm.Info.Println("Build command:", compiler, commandPrettifier(commands.AsSlice()))
	}

	stdout, stderr, err := shell.RunCommandWithEnv(envCompile, projectData.Path, compiler, commands.AsSlice()...)
	if err != nil {
		return nil, fmt.Errorf("the WASM backend compile failed:\n%s\n%s", stdout, stderr)
	}
	result.WasmPath = wasmPath

	// Probe the compiled object: extract and verify the real bindings catalog
	// and install the browser bootstrap (wasm-bridge.js, wails-runtime.js,
	// go-exec.js) into wailsjs/wasm. Generation mode exits cleanly, leaving
	// the object valid for normal bridging in the browser.
	_, err = webbindings.GenerateWebBindings(webbindings.Options{
		Compiler:         options.Compiler,
		Tags:             compileTags,
		ProjectDirectory: projectData.Path,
		ObjectPath:       wasmPath,
	})
	if err != nil {
		return nil, fmt.Errorf("the WASM bindings probe failed: %w", err)
	}

	// Sanity-check the dist output ------------------------------------------
	warnings, err := verifyWebDist(dist)
	if err != nil {
		return nil, err
	}
	for _, warning := range warnings {
		pterm.Warning.Println(warning)
	}

	if stats, err := os.Stat(wasmPath); err == nil {
		pterm.Info.Printf("WASM backend: %s (%.1f MB)\n", wasmPath, float64(stats.Size())/(1024*1024))
	}
	pterm.Info.Println("Web app built in: " + dist)
	return result, nil
}

// ServeWebApp serves the built web application from a dist directory over
// plain HTTP (stdlib MIME table already knows .wasm; the handler pins the
// content type regardless of local mime.types databases). It blocks until
// the listener fails.
func ServeWebApp(dist, addr string) error {
	if !fs.DirExists(dist) {
		return fmt.Errorf("cannot serve the web app: directory '%s' does not exist", dist)
	}
	fileServer := http.FileServer(http.Dir(dist))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(strings.ToLower(r.URL.Path), ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}
		fileServer.ServeHTTP(w, r)
	})
	pterm.Info.Printf("Serving %s at http://%s (Ctrl+C to stop)\n", dist, addr)
	return http.ListenAndServe(addr, handler)
}

// webCompileTags filters user build tags for the js/wasm compile: the
// webbridge tag is added by the pipeline itself and desktop-only tags are
// dropped with a warning.
func webCompileTags(userTags []string) (tags []string, warnings []string) {
	tags = lo.Without(append([]string{}, userTags...), "webbridge")
	keep := make([]string, 0, len(tags))
	for _, tag := range tags {
		if lo.Contains(webDroppedTags, tag) {
			warnings = append(warnings, fmt.Sprintf("dropping build tag '%s' (not applicable to the web/wasm target)", tag))
			continue
		}
		keep = append(keep, tag)
	}
	return keep, warnings
}

// locateWebDist resolves the directory a browser should be served from.
// Conventional projects embed `frontend/dist`; custom layouts may embed a
// package-local dist directory instead.
func locateWebDist(projectData *project.Project) (string, error) {
	var candidates []string

	frontendDist := filepath.Join(projectData.GetFrontendDir(), "dist")
	if fs.DirExists(frontendDist) {
		candidates = append(candidates, frontendDist)
	}

	embedDetails, err := staticanalysis.GetEmbedDetails(projectData.Path)
	if err == nil {
		for _, detail := range embedDetails {
			if filepath.Base(detail.EmbedPath) != "dist" {
				continue
			}
			embedDist := detail.GetFullPath()
			exists := fs.DirExists(embedDist)
			duplicate := lo.ContainsBy(candidates, func(candidate string) bool {
				absCandidate, _ := filepath.Abs(candidate)
				absEmbedDist, _ := filepath.Abs(embedDist)
				return absCandidate == absEmbedDist
			})
			if exists && !duplicate {
				candidates = append(candidates, embedDist)
			}
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("cannot locate the frontend dist directory: '%s' does not exist and no embedded dist directory was found — build the frontend first", frontendDist)
	}
	return candidates[0], nil
}

// verifyWebDist sanity-checks the built web app and returns non-fatal
// warnings. It guarantees the wasm object is valid and that the dist bundle
// references the bootstrap (either bundled by a bundler or plain-served).
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

	bridgeFound, runtimeFound := scanWebDistMarkers(dist)
	plainBridge := filepath.Join(dist, "wailsjs", "wasm", "wasm-bridge.js")
	if !bridgeFound && !fs.FileExists(plainBridge) {
		warnings = append(warnings, "the dist bundle does not reference the WASM bootstrap — import it at the top of your frontend entry point: `import '<wailsjs>/wasm/wasm-bridge.js'` (then `import '<wailsjs>/wasm/wails-runtime.js'`) and rebuild")
	}
	if !runtimeFound {
		warnings = append(warnings, "the dist bundle does not reference the Wails runtime (`"+runtimeMarker+"`) — import `<wailsjs>/wasm/wails-runtime.js` after the bridge so window.runtime/window.wails are installed")
	}
	return warnings, nil
}

// scanWebDistMarkers walks the dist directory (text files only) and reports
// whether the wasm-bridge and the runtime bundle markers are present.
func scanWebDistMarkers(dist string) (bridgeFound bool, runtimeFound bool) {
	skipDirs := []string{"node_modules", ".git"}
	_ = filepath.WalkDir(dist, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries never fail the build
		}
		if d.IsDir() {
			if lo.Contains(skipDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".wasm") {
			return nil // our own binary output, always marker-free
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// HTML filters (minifiers) may escape some bytes; the markers are
		// plain ASCII so a raw substring check is sufficient.
		if bytes.Contains(data, []byte(bridgeMarker)) {
			bridgeFound = true
		}
		if bytes.Contains(data, []byte(runtimeMarker)) {
			runtimeFound = true
		}
		return nil
	})
	return bridgeFound, runtimeFound
}

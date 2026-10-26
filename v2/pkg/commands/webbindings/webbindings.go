package webbindings

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/samber/lo"
	"github.com/leaanthony/gosod"
	"github.com/wailsapp/wails/v2/internal/fs"
	"github.com/wailsapp/wails/v2/internal/frontend/runtime/webwrapper"
	"github.com/wailsapp/wails/v2/internal/project"
	"github.com/wailsapp/wails/v2/internal/shell"
	"github.com/wailsapp/wails/v2/pkg/commands/buildtags"
)

// Sentinel must match internal/app/app_webbridge.go (kept literal so the CLI
// does not have to pull the app package) — the generator wasm prints one line
// "< sentinel > {json}" when WAILS_WEB_BINDINGS=1.
const sentinel = "wailsweb-bindings: "

// jsFuncRegex is the shape emitted into wailsjs/go/<ns>/<Struct>.js for every
// bound method (`export function Foo(arg1, arg2) { ... }`). It drives the
// parity cross-check between the wasm catalog and the desktop bindings.
var jsFuncRegex = regexp.MustCompile(`export function ([A-Za-z0-9_]+)\(`)

//go:embed wasm-runner.cjs
var wasmRunnerJS []byte

// Options for generating web (WASM) bindings.
type Options struct {
	Compiler         string
	Tags             []string
	ProjectDirectory string
	SkipVerify       bool
	PrintJSON        bool
	// ObjectPath, when set, points at an already-compiled js/wasm webbridge
	// module (e.g. the final `wails build -platform web` artifact). The
	// engine then skips its own compilation and probes that object instead,
	// leaving it untouched for use in the browser.
	ObjectPath string
}

// Catalog is the parsed generator output.
type Catalog struct {
	Services []Service `json:"services"`
}

// Service is one bound service in the catalog.
type Service struct {
	Namespace string   `json:"ns"`
	Struct    string   `json:"name"`
	Methods   []string `json:"methods"`
}

// GenerateWebBindings builds the project as a js/wasm module, runs it under
// Node to reflect the real Bind list in the wasm build, verifies the result
// against the desktop-generated wailsjs files and installs the browser
// runtime bootstrap.
func GenerateWebBindings(options Options) (Catalog, error) {
	catalog := Catalog{}
	workingDirectory, _ := lo.Coalesce(options.ProjectDirectory, lo.Must(os.Getwd()))

	projectConfig, err := project.Load(workingDirectory)
	if err != nil {
		return catalog, err
	}
	// Desktop parity: `wails generate module` writes to
	// <GetWailsJSDir()>/wailsjs/{go,runtime} — the web artifacts belong in the
	// same tree (<...>/wailsjs/wasm) and the parity check reads
	// <...>/wailsjs/go.
	wailsjsBaseDir := filepath.Join(projectConfig.GetWailsJSDir(), "wailsjs")
	wailsjsDir := filepath.Join(wailsjsBaseDir, "go")

	tags := append(options.Tags, "webbridge")
	webTags := lo.Without(tags, "desktop", "production", "debug", "dev")
	tagString := buildtags.Stringify(webTags)

	// Build the js/wasm module (unless the caller supplies the final
	// artifact, e.g. `wails build -platform web` compiles it itself and
	// reuses it for the probe) --------------------------------------------
	compiler := lo.CoalesceOrEmpty(options.Compiler, "go")
	tempObjectPath := ""
	if options.ObjectPath == "" {
		tempFile, err := os.CreateTemp(os.TempDir(), "wails-webbindings-*.wasm")
		if err != nil {
			return catalog, err
		}
		tempObjectPath = tempFile.Name()
		_ = tempFile.Close()
		defer func() { _ = os.Remove(tempObjectPath) }()

		envBuild := os.Environ()
		envBuild = shell.SetEnv(envBuild, "GOOS", "js")
		envBuild = shell.SetEnv(envBuild, "GOARCH", "wasm")
		envBuild = shell.SetEnv(envBuild, "CGO_ENABLED", "0")
		envBuild = shell.RemoveEnv(envBuild, "CC")

		stdout, stderr, err := shell.RunCommandWithEnv(envBuild, workingDirectory, compiler,
			"build", "-buildvcs=false", "-tags", tagString, "-o", tempObjectPath, ".")
		if err != nil {
			return catalog, fmt.Errorf("failed to build the WASM backend with the webbridge tag:\n%s\n%s", stdout, stderr)
		}
	}
	objectPath := lo.CoalesceOrEmpty(options.ObjectPath, tempObjectPath)

	// Run it under Node and harvest the bindings database -----------------------
	nodePath, nodeErr := exec.LookPath("node")
	if nodeErr != nil {
		return catalog, fmt.Errorf("`wails generate webbindings` needs Node (it runs the WASM bindings probe); install Node or add it to PATH")
	}
	goroot, goRootErr := queryGoRoot(workingDirectory, compiler)
	if goRootErr != nil {
		return catalog, goRootErr
	}
	wasmExecPath := filepath.Join(goroot, "lib", "wasm", "wasm_exec.js")
	if !fs.FileExists(wasmExecPath) {
		return catalog, fmt.Errorf("cannot find %s — is the js/wasm toolchain installed for this Go version?", wasmExecPath)
	}

	sentinelJSON, runOutput, err := runBindingsProbe(nodePath, wasmExecPath, objectPath, workingDirectory, options)
	if err != nil {
		return catalog, err
	}

	var rawStore map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(sentinelJSON), &rawStore); err != nil {
		return catalog, fmt.Errorf("cannot parse the WASM bindings catalog: %w", err)
	}

	namespaces := lo.Keys(rawStore)
	sort.Strings(namespaces)
	for _, namespace := range namespaces {
		structures := rawStore[namespace]
		structNames := lo.Keys(structures)
		sort.Strings(structNames)
		for _, structName := range structNames {
			methodNames := lo.Keys(structures[structName])
			sort.Strings(methodNames)
			catalog.Services = append(catalog.Services, Service{
				Namespace: namespace,
				Struct:    structName,
				Methods:   methodNames,
			})
		}
	}

	if options.PrintJSON {
		pretty, _ := json.MarshalIndent(catalog.Services, "", "  ")
		fmt.Println(string(pretty))
	}

	// Cross-check against the desktop-generated wailsjs bindings ---------------
	if !options.SkipVerify {
		warnings := verifyAgainstWailsJS(catalog, wailsjsDir)
		for _, warning := range warnings {
			fmt.Println()
			fmt.Printf("  ⚠ %s\n", warning)
		}
	}

	// Install the browser bootstrap --------------------------------------------
	if err := InstallWebBootstrap(workingDirectory, compiler); err != nil {
		return catalog, err
	}

	_ = runOutput // remain silent when everything is fine
	return catalog, nil
}

// InstallWebBootstrap copies the browser bootstrap into
// <WailsJSDir>/wailsjs/wasm: the embedded wasm-bridge.js, wails-runtime.js and
// WASM_README.txt, plus the compiler toolchain's lib/wasm/wasm_exec.js as
// go-exec.js. It is safe to run before the frontend build (a vite build
// resolves `wailsjs/wasm/*` imports) and is idempotent.
func InstallWebBootstrap(projectDirectory, compiler string) error {
	workingDirectory, _ := lo.Coalesce(projectDirectory, lo.Must(os.Getwd()))

	projectConfig, err := project.Load(workingDirectory)
	if err != nil {
		return err
	}
	// Desktop parity: the web artifacts live next to the generated bindings
	// (<...>/wailsjs/wasm).
	wasmDir := filepath.Join(projectConfig.GetWailsJSDir(), "wailsjs", "wasm")
	if err := fs.MkDirs(wasmDir); err != nil {
		return err
	}
	extractor := gosod.New(webwrapper.WebWrapper)
	if err := extractor.Extract(wasmDir, nil); err != nil {
		return err
	}

	compilerName := lo.CoalesceOrEmpty(compiler, "go")
	goroot, goRootErr := queryGoRoot(workingDirectory, compilerName)
	if goRootErr != nil {
		return goRootErr
	}
	wasmExecPath := filepath.Join(goroot, "lib", "wasm", "wasm_exec.js")
	if !fs.FileExists(wasmExecPath) {
		return fmt.Errorf("cannot find %s — is the js/wasm toolchain installed for this Go version?", wasmExecPath)
	}
	execSource, err := os.ReadFile(wasmExecPath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(wasmDir, "go-exec.js"), execSource, 0o644)
}

// runBindingsProbe executes the wasm module under Node with
// WAILS_WEB_BINDINGS=1 and extracts the sentinel JSON line from stdout.
func runBindingsProbe(nodePath, wasmExecPath, objectPath, workingDirectory string, options Options) (string, string, error) {
	runnerPath := filepath.Join(os.TempDir(), "wails-wasm-runner.cjs")
	if err := os.WriteFile(runnerPath, wasmRunnerJS, 0o644); err != nil {
		return "", "", fmt.Errorf("cannot stage the wasm probe runner: %w", err)
	}
	defer func() { _ = os.Remove(runnerPath) }()

	cmd := exec.Command(nodePath, runnerPath, wasmExecPath, objectPath)
	cmd.Dir = workingDirectory
	env := append(os.Environ(), "WAILS_WEB_BINDINGS=1")
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if runErr != nil || stderr.Len() > 0 {
		message := extractSentinel(stdout.String())
		if message != "" && runErr == nil {
			// Non-fatal stderr noise while a catalog was still produced.
			return message, stderr.String(), nil
		}
		return "", "", fmt.Errorf("the WASM bindings probe failed:\n%s", stderr.String())
	}

	message := extractSentinel(stdout.String())
	if message == "" {
		return "", "", fmt.Errorf("the WASM bindings probe produced no catalog (missing %q line on stdout)", sentinel)
	}
	return message, stderr.String(), nil
}

func extractSentinel(stdout string) string {
	for _, line := range strings.Split(stdout, "\n") {
		trimmed := strings.TrimRight(line, " \r\t")
		if strings.HasPrefix(trimmed, sentinel) {
			return strings.TrimSpace(trimmed[len(sentinel):])
		}
	}
	return ""
}

// queryGoRoot resolves the GOROOT of the selected compiler so the
// runtime assets (lib/wasm/wasm_exec.js) always come from the exact
// toolchain that built the backend module.
func queryGoRoot(workingDirectory, compiler string) (string, error) {
	if envGoRoot := os.Getenv("GOROOT"); envGoRoot != "" {
		return filepath.Clean(envGoRoot), nil
	}
	stdout, stderr, err := shell.RunCommand(workingDirectory, compiler, "env", "GOROOT")
	if err != nil {
		return "", fmt.Errorf("cannot resolve GOROOT: %w\n%s\n%s", err, stdout, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	root := strings.TrimSpace(lines[len(lines)-1])
	if root == "" {
		return "", fmt.Errorf("`%s env GOROOT` returned an empty value", compiler)
	}
	return filepath.Clean(root), nil
}

// verifyAgainstWailsJS returns warnings where the wasm catalog and the
// desktop-generated bindings disagree. A missing desktop file is not an
// error ("wails generate module" may not have run yet) — only it is noted.
func verifyAgainstWailsJS(catalog Catalog, desktopBindingsDir string) []string {
	var warnings []string
	if !fs.DirExists(desktopBindingsDir) {
		warnings = append(warnings, fmt.Sprintf("no desktop bindings found at %s (run `wails generate module`) — parity check skipped", desktopBindingsDir))
		return warnings
	}
	for _, service := range catalog.Services {
		comparable := filepath.Join(desktopBindingsDir, service.Namespace, service.Struct+".js")
		if !fs.FileExists(comparable) {
			warnings = append(warnings, fmt.Sprintf("missing desktop binding %s — service only exists on the web build", comparable))
			continue
		}
		data, err := os.ReadFile(comparable)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot read %s: %v", comparable, err))
			continue
		}
		desktop := map[string]bool{}
		for _, match := range jsFuncRegex.FindAllStringSubmatch(string(data), -1) {
			desktop[match[1]] = true
		}
		wants := map[string]bool{}
		for _, method := range service.Methods {
			wants[method] = true
		}
		for name := range desktop {
			if !wants[name] {
				warnings = append(warnings, fmt.Sprintf("%s: desktop-only method %s (no %s variant in the wasm build)", comparable, name, service.Namespace+"."+service.Struct+"."+name))
			}
		}
		for name := range wants {
			if !desktop[name] {
				warnings = append(warnings, fmt.Sprintf("%s: wasm-only method %s — regenerate desktop bindings", comparable, name))
			}
		}
	}
	return warnings
}

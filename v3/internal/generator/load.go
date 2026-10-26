package generator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"

	"github.com/wailsapp/wails/v3/internal/generator/config"
	"golang.org/x/tools/go/packages"
)

// ResolveSystemPaths resolves paths for stdlib and Wails packages.
func ResolveSystemPaths(buildFlags []string) (paths *config.SystemPaths, err error) {
	return ResolveSystemPathsInEnv(nil, buildFlags)
}

// ResolveSystemPathsInEnv resolves paths for stdlib and Wails packages
// within the given environment.
// A nil env inherits the environment of the current process.
func ResolveSystemPathsInEnv(env []string, buildFlags []string) (paths *config.SystemPaths, err error) {
	// Resolve std context pkg path.
	contextPkgPaths, err := ResolvePatternsInEnv(env, buildFlags, "context")
	if err != nil {
		return
	} else if len(contextPkgPaths) < 1 {
		err = ErrNoContextPackage
		return
	} else if len(contextPkgPaths) > 1 {
		// This should never happen...
		panic("context package path matched multiple packages")
	}

	// Resolve std time pkg path.
	timePkgPaths, err := ResolvePatternsInEnv(env, buildFlags, "time")
	if err != nil {
		return
	} else if len(timePkgPaths) < 1 {
		err = ErrNoTimePackage
		return
	} else if len(timePkgPaths) > 1 {
		// This should never happen...
		panic("time package path matched multiple packages")
	}

	// Resolve wails app pkg path.
	wailsAppPkgPaths, err := ResolvePatternsInEnv(env, buildFlags, config.WailsAppPkgPath)
	if err != nil {
		return
	} else if len(wailsAppPkgPaths) < 1 {
		err = ErrNoApplicationPackage
		return
	} else if len(wailsAppPkgPaths) > 1 {
		// This should never happen...
		panic("wails application package path matched multiple packages")
	}

	// Resolve wails internal pkg path.
	wailsInternalPkgPaths, err := ResolvePatternsInEnv(env, buildFlags, config.WailsInternalPkgPath)
	if err != nil {
		return
	} else if len(wailsInternalPkgPaths) < 1 {
		err = ErrNoInternalPackage
		return
	} else if len(wailsInternalPkgPaths) > 1 {
		// This should never happen...
		panic("wails internal package path matched multiple packages")
	}

	paths = &config.SystemPaths{
		ContextPackage:     contextPkgPaths[0],
		TimePackage:        timePkgPaths[0],
		ApplicationPackage: wailsAppPkgPaths[0],
		InternalPackage:    wailsInternalPkgPaths[0],
	}
	return
}

// ResolvePatterns returns a slice containing all package paths
// that match the given patterns, according to the underlying build tool
// and within the context of the current working directory.
func ResolvePatterns(buildFlags []string, patterns ...string) (paths []string, err error) {
	return ResolvePatternsInEnv(nil, buildFlags, patterns...)
}

// ResolvePatternsInEnv returns a slice containing all package paths
// that match the given patterns, according to the underlying build tool,
// resolved within the given environment.
// A nil env inherits the environment of the current process.
func ResolvePatternsInEnv(env []string, buildFlags []string, patterns ...string) (paths []string, err error) {
	rewrittenPatterns := make([]string, len(patterns))
	for i, pattern := range patterns {
		rewrittenPatterns[i] = "pattern=" + pattern
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode:       packages.NeedName,
		BuildFlags: buildFlags,
		Env:        env,
	}, rewrittenPatterns...)

	for _, pkg := range pkgs {
		paths = append(paths, pkg.PkgPath)
	}

	return
}

// LoadPackages loads the packages specified by the given patterns
// and their whole dependency tree. It returns a slice containing
// all packages that match the given patterns and all of their direct
// and indirect dependencies.
//
// The returned slice is in post-order w.r.t. the dependency relation,
// i.e. if package A depends on package B, then package B precedes package A.
//
// All returned package instances include syntax trees and full type information.
//
// Syntax is loaded in the context of a global [token.FileSet],
// which is available through the field [packages.Package.Fset]
// on each returned package. Therefore, source positions
// are canonical across all loaded packages.
func LoadPackages(buildFlags []string, patterns ...string) (pkgs []*packages.Package, err error) {
	return LoadPackagesInEnv(nil, buildFlags, patterns...)
}

// LoadPackagesInEnv loads the packages specified by the given patterns
// and their whole dependency tree within the given environment.
// A nil env inherits the environment of the current process.
//
// See [LoadPackages] for details on the return value and
// the loading modes.
func LoadPackagesInEnv(env []string, buildFlags []string, patterns ...string) (pkgs []*packages.Package, err error) {
	rewrittenPatterns := make([]string, len(patterns))
	for i, pattern := range patterns {
		rewrittenPatterns[i] = "pattern=" + pattern
	}

	// Global file set.
	fset := token.NewFileSet()

	roots, err := packages.Load(&packages.Config{
		// NOTE: some Go maintainers now believe deprecation was an error and recommend using Load* modes
		// (see e.g. https://github.com/golang/go/issues/48226#issuecomment-1948792315).
		Mode:       packages.LoadAllSyntax,
		BuildFlags: buildFlags,
		Env:        env,
		Fset:       fset,
		ParseFile: func(fset *token.FileSet, filename string, src []byte) (file *ast.File, err error) {
			file, err = parser.ParseFile(fset, filename, src, parser.ParseComments|parser.SkipObjectResolution)
			return
		},
	}, rewrittenPatterns...)

	// Flatten dependency tree.
	packages.Visit(roots, nil, func(pkg *packages.Package) {
		if pkg.Fset != fset {
			panic("fileset missing or not the global one")
		}
		pkgs = append(pkgs, pkg)
	})

	return
}

// WasmPackageEnv returns an environment list configuring the Go build
// tooling for the js/wasm (web) target. It is used by
// `wails3 generate bindings --wasm` so that `_wasm` build variants and
// web-only packages are analysed, mirroring the environment
// `wails3 build` uses for the web backend module.
func WasmPackageEnv() []string {
	env := os.Environ()
	env = setEnvKeys(env, map[string]string{
		"GOOS":        "js",
		"GOARCH":      "wasm",
		"CGO_ENABLED": "0",
	})
	return env
}

// setEnvKeys replaces the given keys in the environment list,
// appending them when absent.
func setEnvKeys(env []string, keys map[string]string) []string {
	result := make([]string, 0, len(env)+len(keys))
	var replaced = make(map[string]bool, len(keys))
	for _, entry := range env {
		index := strings.IndexByte(entry, '=')
		if index < 0 {
			result = append(result, entry)
			continue
		}
		if value, found := keys[entry[:index]]; found {
			result = append(result, entry[:index]+"="+value)
			replaced[entry[:index]] = true
			continue
		}
		result = append(result, entry)
	}
	for key, value := range keys {
		if !replaced[key] {
			result = append(result, key+"="+value)
		}
	}
	return result
}

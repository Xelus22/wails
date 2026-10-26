package generator

import (
	"encoding/json"
	"errors"
	"go/types"
	"io"
	"runtime"
	"sort"

	"github.com/wailsapp/wails/v3/internal/generator/config"
)

// compareAgainstNative re-runs the binding analysis against the native
// (desktop) environment and reports binding surface differences against
// the wasm results produced by the current generator run.
//
// The goal is a single bindings tree (see the web integration plan):
// the runtime transport layer decides at load time whether built assets
// speak the desktop IPC or the wasm bridge, so both targets must see the
// same generated files. Any drift is reported as a warning with guidance
// on build-tag hygiene.
func (generator *Generator) compareAgainstNative(patterns []string, buildFlags []string) {
	options := *generator.options
	options.Wasm = false
	options.WasmCatalog = false

	comparator := NewGenerator(&options, config.NullCreator, config.NullLogger)
	_, err := comparator.Generate(patterns...)
	if err != nil {
		var report *ErrorReport
		switch {
		case errors.Is(err, ErrNoPackages):
			generator.logger.Warningf("wasm/native binding comparison skipped: the native analysis found no packages")
		case errors.As(err, &report):
			if report.HasErrors() {
				generator.logger.Warningf("wasm/native binding comparison skipped: the native analysis failed (%d error(s)). Re-run with -v for details", len(report.Errors()))
				generator.logger.Debugf("%v", report)
				return
			}
			// Warnings only: the native analysis is still usable.
			generator.logger.Debugf("native analysis reported %d warning(s): %v", len(report.Warnings()), report)
		default:
			generator.logger.Warningf("wasm/native binding comparison skipped: the native analysis failed: %v", err)
			return
		}
	}

	wasmSurface := generator.bindingSurface()
	nativeSurface := comparator.bindingSurface()
	nativeOnly, wasmOnly := diffBindingSurfaces(wasmSurface, nativeSurface)

	for _, fqn := range nativeOnly {
		generator.logger.Warningf(
			"%s: native-only binding: the js/wasm build cannot call it. Provide a *_wasm.go implementation or review the build tags guarding it",
			fqn,
		)
	}
	for _, fqn := range wasmOnly {
		generator.logger.Warningf(
			"%s: js/wasm-only binding: it will not be callable in a desktop build. Move it out of the js-only build tags or provide a native counterpart to prevent drift",
			fqn,
		)
	}
	if len(nativeOnly) == 0 && len(wasmOnly) == 0 {
		generator.logger.Debugf("wasm and native binding surfaces are identical")
	}
}

// bindingSurface maps every bount method fully qualified name to its
// internal flag, as recorded by the current generator's collector.
func (generator *Generator) bindingSurface() map[string]bool {
	surface := make(map[string]bool)
	for pkg := range generator.collector.Iterate {
		for service := range pkg.Services {
			for _, method := range service.Collect().Methods {
				surface[method.FQN] = method.Internal
			}
		}
	}
	return surface
}

// diffBindingSurfaces returns the method IDs present only in the native
// set and the ones present only in the wasm set, both sorted.
// The internal flag is ignored: internal methods are symmetric decisions
// in both environments and carry no callable semantics.
func diffBindingSurfaces(wasm, native map[string]bool) (nativeOnly, wasmOnly []string) {
	for fqn := range native {
		if _, found := wasm[fqn]; !found {
			nativeOnly = append(nativeOnly, fqn)
		}
	}
	for fqn := range wasm {
		if _, found := native[fqn]; !found {
			wasmOnly = append(wasmOnly, fqn)
		}
	}
	sort.Strings(nativeOnly)
	sort.Strings(wasmOnly)
	return
}

// wasmCatalog describes the bindings of one web service.
type wasmCatalog struct {
	// Environment of the analysis the catalog was derived from.
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`

	// Services is the flat list of discovered services sorted by fqn.
	Services []wasmCatalogService `json:"services"`
}

// wasmCatalogService describes the bindings of one service type.
type wasmCatalogService struct {
	// FQN fully qualifies the service as packagePath.StructName
	// ("main" stands in for the path of main packages).
	FQN string `json:"fqn"`

	// Methods lists callable methods, sorted by fqn. Includes internal
	// methods as flagged entries: obfuscated bindings and the web runtime
	// still need their IDs.
	Methods []wasmCatalogMethod `json:"methods"`
}

// wasmCatalogMethod describes one wired method binding.
type wasmCatalogMethod struct {
	FQN      string `json:"fqn"`
	ID       string `json:"id"`
	Internal bool   `json:"internal,omitempty"`
}

// generateWasmCatalog emits `wasm.catalog.json` into the output directory.
// It is the machine-readable transcript of the analysed (wasm or native)
// binding surface, consumed by the v3 web runtime harness and CI parity jobs.
func (generator *Generator) generateWasmCatalog() {
	catalog := wasmCatalog{
		GOOS:   "js",
		GOARCH: "wasm",
	}
	if !generator.options.Wasm {
		// Cataloguing the native analysis: record the actual target.
		catalog.GOOS = runtime.GOOS
		catalog.GOARCH = runtime.GOARCH
	}

	for pkg := range generator.collector.Iterate {
		for service := range pkg.Services {
			collected := service.Collect()
			obj, ok := collected.Object().(*types.TypeName)
			if !ok {
				continue
			}
			path := obj.Pkg().Path()
			if obj.Pkg().Name() == "main" {
				// FQN normalises main packages for the same reason as
				// collectMethod does.
				path = "main"
			}
			entry := wasmCatalogService{
				FQN:     path + "." + obj.Name(),
				Methods: make([]wasmCatalogMethod, 0, len(collected.Methods)),
			}
			for _, method := range collected.Methods {
				entry.Methods = append(entry.Methods, wasmCatalogMethod{
					FQN:      method.FQN,
					ID:       method.ID,
					Internal: method.Internal,
				})
			}
			catalog.Services = append(catalog.Services, entry)
		}
	}

	// Deterministic output regardless of map iteration order.
	sort.Slice(catalog.Services, func(i, j int) bool {
		return catalog.Services[i].FQN < catalog.Services[j].FQN
	})
	for i := range catalog.Services {
		methods := catalog.Services[i].Methods
		sort.Slice(methods, func(a, b int) bool {
			return methods[a].FQN < methods[b].FQN
		})
	}

	writer, err := generator.creator.Create("wasm.catalog.json")
	if err != nil {
		generator.logger.Errorf("cannot write wasm.catalog.json: %v", err)
		return
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(catalog); err != nil {
		generator.logger.Errorf("cannot encode wasm.catalog.json: %v", err)
	}
	if closer, ok := writer.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			generator.logger.Errorf("cannot close wasm.catalog.json: %v", err)
		}
	}
}

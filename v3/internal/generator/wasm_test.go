package generator

import (
	"reflect"
	"testing"
)

func TestDiffBindingSurfaces(t *testing.T) {
	cases := []struct {
		name       string
		wasm       map[string]bool
		native     map[string]bool
		nativeOnly []string
		wasmOnly   []string
	}{
		{
			name:   "identical surfaces",
			wasm:   map[string]bool{"main.Settings.GetTheme": false},
			native: map[string]bool{"main.Settings.GetTheme": true},
		},
		{
			name:       "mixed drift",
			wasm:       map[string]bool{"main.Settings.GetTheme": false, "main.WebOnly.FlushQueue": false},
			native:     map[string]bool{"main.Settings.GetTheme": true, "main.DesktopOnly.EjectDisc": false},
			nativeOnly: []string{"main.DesktopOnly.EjectDisc"},
			wasmOnly:   []string{"main.WebOnly.FlushQueue"},
		},
		{
			name:       "empty wasm surface",
			native:     map[string]bool{"main.Settings.GetTheme": true},
			nativeOnly: []string{"main.Settings.GetTheme"},
		},
		{
			name:     "empty native surface",
			wasm:     map[string]bool{"main.Settings.GetTheme": false},
			wasmOnly: []string{"main.Settings.GetTheme"},
		},
		{
			name:  "nothing at all",
			wasm:  map[string]bool{},
			native: map[string]bool{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nativeOnly, wasmOnly := diffBindingSurfaces(tc.wasm, tc.native)
			if len(nativeOnly) != 0 || len(wasmOnly) != 0 {
				if !reflect.DeepEqual(nativeOnly, tc.nativeOnly) || !reflect.DeepEqual(wasmOnly, tc.wasmOnly) {
					t.Fatalf("diffBindingSurfaces() = (%v, %v), want (%v, %v)",
						nativeOnly, wasmOnly, tc.nativeOnly, tc.wasmOnly)
				}
				for _, fqn := range nativeOnly {
					if _, found := tc.wasm[fqn]; found {
						t.Fatalf("%s reported native-only but is present in the wasm surface", fqn)
					}
				}
				for _, fqn := range wasmOnly {
					if _, found := tc.native[fqn]; found {
						t.Fatalf("%s reported wasm-only but is present in the native surface", fqn)
					}
				}
				return
			}
			if len(tc.nativeOnly) != 0 || len(tc.wasmOnly) != 0 {
				t.Fatalf("diffBindingSurfaces() no drift, want (%v, %v)", tc.nativeOnly, tc.wasmOnly)
			}
		})
	}
}

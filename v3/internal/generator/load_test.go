package generator

import (
	"reflect"
	"testing"
)

func TestSetEnvKeys(t *testing.T) {
	cases := []struct {
		name string
		env  []string
		keys map[string]string
		want []string
	}{
		{
			name: "replace existing values",
			env:  []string{"HOME=/x", "GOOS=linux", "PATH=/bin"},
			keys: map[string]string{"GOOS": "js", "GOARCH": "wasm"},
			want: []string{"HOME=/x", "GOOS=js", "PATH=/bin", "GOARCH=wasm"},
		},
		{
			name: "append missing keys",
			env:  []string{"HOME=/x"},
			keys: map[string]string{"GOOS": "js", "GOARCH": "wasm", "CGO_ENABLED": "0"},
			want: []string{"HOME=/x", "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0"},
		},
		{
			name: "preserve entries without an equal sign",
			env:  []string{"notanentry"},
			keys: map[string]string{"GOOS": "js"},
			want: []string{"notanentry", "GOOS=js"},
		},
		{
			name: "empty environment",
			env:  []string{},
			keys: map[string]string{"GOOS": "js"},
			want: []string{"GOOS=js"},
		},
		{
			name: "only the targeted keys change",
			env:  []string{"CGO_ENABLED=1", "CC=clang"},
			keys: map[string]string{"CGO_ENABLED": "0"},
			want: []string{"CGO_ENABLED=0", "CC=clang"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := setEnvKeys(tc.env, tc.keys)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("setEnvKeys() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWasmPackageEnv(t *testing.T) {
	t.Setenv("GOOS", "linux")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("CGO_ENABLED", "1")

	env := WasmPackageEnv()

	find := func(key string) (string, bool) {
		for _, entry := range env {
			if len(entry) > len(key) && entry[:len(key)+1] == key+"=" {
				return entry[len(key)+1:], true
			}
		}
		return "", false
	}

	if got, _ := find("GOOS"); got != "js" {
		t.Fatalf("GOOS = %q, want js", got)
	}
	if got, _ := find("GOARCH"); got != "wasm" {
		t.Fatalf("GOARCH = %q, want wasm", got)
	}
	if got, _ := find("CGO_ENABLED"); got != "0" {
		t.Fatalf("CGO_ENABLED = %q, want 0", got)
	}
	if got, _ := find("HOME"); got == "" {
		t.Fatalf("non-targeted entries were not preserved")
	}
}

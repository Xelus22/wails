package webbindings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestExtractSentinel(t *testing.T) {
	cases := []struct {
		name   string
		stdout string
		want   string
	}{
		{
			name:   "catalog line",
			stdout: "noise\n" + sentinel + `{"main":{"Settings":{}}}` + "\nmore noise\n",
			want:   `{"main":{"Settings":{}}}`,
		},
		{
			name:   "trailing carriage return",
			stdout: sentinel + `{"a":1}` + "\r\n",
			want:   `{"a":1}`,
		},
		{
			name:   "only the first occurrence is used",
			stdout: sentinel + `{"first":1}` + "\n" + sentinel + `{"second":2}` + "\n",
			want:   `{"first":1}`,
		},
		{
			name:   "absent",
			stdout: "some other program output\n",
			want:   "",
		},
		{
			name:   "prefix inside a word does not count",
			stdout: "nowailsweb-bindings: {}",
			want:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractSentinel(tc.stdout)
			if got != tc.want {
				t.Fatalf("extractSentinel() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatalogFromStore(t *testing.T) {
	raw := `{"main":{"Dummy":{"Ping":{},"NoResults":{}},"Settings":{"GetTheme":{}}},"other":{"Svc":{"X":{}}}}`
	var store map[string]map[string]map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &store); err != nil {
		t.Fatal(err)
	}
	catalog := Catalog{}
	namespaces := sortedKeys(store)
	for _, ns := range namespaces {
		for structName := range store[ns] {
			names := sortedKeys(store[ns][structName])
			catalog.Services = append(catalog.Services, Service{Namespace: ns, Struct: structName, Methods: names})
		}
	}
	if len(catalog.Services) != 3 {
		t.Fatalf("expected 3 services, got %d", len(catalog.Services))
	}
	// Deterministic ordering: namespaces, then structs, then methods.
	if catalog.Services[0].Namespace != "main" || catalog.Services[0].Struct != "Dummy" {
		t.Fatalf("unexpected first service: %+v", catalog.Services[0])
	}
	first := catalog.Services[0]
	if first.Methods[0] != "NoResults" || first.Methods[1] != "Ping" {
		t.Fatalf("methods not sorted: %+v", first.Methods)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func writeDesktopBinding(t *testing.T, dir, namespace, structName, methods string) string {
	t.Helper()
	dirName := filepath.Join(dir, namespace)
	if err := os.MkdirAll(dirName, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dirName, structName+".js")
	content := "// generated\n" + methods
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestVerifyAgainstWailsJS(t *testing.T) {
	t.Run("matching bindings produce no warnings", func(t *testing.T) {
		dir := t.TempDir()
		writeDesktopBinding(t, dir, "main", "Dummy", "export function Ping() {\n}\n")
		catalog := Catalog{Services: []Service{{Namespace: "main", Struct: "Dummy", Methods: []string{"Ping"}}}}
		warnings := verifyAgainstWailsJS(catalog, dir)
		if len(warnings) != 0 {
			t.Fatalf("unexpected warnings: %v", warnings)
		}
	})

	t.Run("desktop-only method is reported", func(t *testing.T) {
		dir := t.TempDir()
		writeDesktopBinding(t, dir, "main", "Dummy", "export function Ping() {\n}\nexport function Legacy() {\n}\n")
		catalog := Catalog{Services: []Service{{Namespace: "main", Struct: "Dummy", Methods: []string{"Ping"}}}}
		warnings := verifyAgainstWailsJS(catalog, dir)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "Legacy") {
			t.Fatalf("expected one warning about Legacy, got %v", warnings)
		}
	})

	t.Run("wasm-only method is reported", func(t *testing.T) {
		dir := t.TempDir()
		writeDesktopBinding(t, dir, "main", "Dummy", "export function Ping() {\n}\n")
		catalog := Catalog{Services: []Service{{Namespace: "main", Struct: "Dummy", Methods: []string{"Ping", "Fresh"}}}}
		warnings := verifyAgainstWailsJS(catalog, dir)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "Fresh") {
			t.Fatalf("expected one warning about Fresh, got %v", warnings)
		}
	})

	t.Run("missing desktop file is reported", func(t *testing.T) {
		dir := t.TempDir()
		writeDesktopBinding(t, dir, "main", "Dummy", "export function Ping() {\n}\n")
		catalog := Catalog{Services: []Service{{Namespace: "main", Struct: "Other", Methods: []string{"Ping"}}}}
		warnings := verifyAgainstWailsJS(catalog, dir)
		if len(warnings) != 1 || !strings.Contains(warnings[0], "missing desktop binding") {
			t.Fatalf("expected a missing-binding warning, got %v", warnings)
		}
	})

	t.Run("absent desktop directories skip the check with a warning", func(t *testing.T) {
		dir := t.TempDir()
		catalog := Catalog{Services: []Service{{Namespace: "main", Struct: "Dummy", Methods: []string{"Ping"}}}}
		warnings := verifyAgainstWailsJS(catalog, filepath.Join(dir, "nope"))
		if len(warnings) != 1 || !strings.Contains(warnings[0], "parity check skipped") {
			t.Fatalf("expected the skip warning, got %v", warnings)
		}
	})
}

func TestQueryGoRootPrefersEnv(t *testing.T) {
	want := t.TempDir()
	t.Setenv("GOROOT", want)
	got, err := queryGoRoot(t.TempDir(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("queryGoRoot() = %q, want the GOROOT env value %q", got, want)
	}
}

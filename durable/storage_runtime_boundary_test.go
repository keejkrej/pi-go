// Ported from packages/durable/test/storage-runtime-boundary.test.ts (pi v1.0.0).

package durable_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func srbPackageImports(t *testing.T, dir string, excludePrefix ...string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q) failed: %v", dir, err)
	}
	fset := token.NewFileSet()
	result := make(map[string][]string)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		skip := false
		for _, prefix := range excludePrefix {
			if strings.HasPrefix(name, prefix) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		fullPath := filepath.Join(dir, name)
		fileAst, err := parser.ParseFile(fset, fullPath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%q) failed: %v", fullPath, err)
		}
		var imps []string
		for _, spec := range fileAst.Imports {
			imps = append(imps, strings.Trim(spec.Path.Value, `"`))
		}
		result[fullPath] = imps
	}
	return result
}

func srbAssertNoImports(t *testing.T, importsByFile map[string][]string, forbidden ...string) {
	t.Helper()
	for file, imps := range importsByFile {
		for _, imp := range imps {
			for _, f := range forbidden {
				if imp == f || strings.HasPrefix(imp, f+"/") {
					t.Errorf("%s imports forbidden package %q", file, imp)
				}
			}
		}
	}
}

func TestDurableStorageRuntimeBoundaries(t *testing.T) {
	rootImports := srbPackageImports(t, ".")
	srbAssertNoImports(t, rootImports,
		"github.com/keejkrej/pi-go/durable/storage",
		"github.com/keejkrej/pi-go/durable/storage/jsonl",
		"github.com/keejkrej/pi-go/durable/storage/sqlite",
		"database/sql",
		"modernc.org/sqlite",
		"os",
		"encoding/json",
		"C",
	)

	memImports := srbPackageImports(t, "storage")
	srbAssertNoImports(t, memImports,
		"github.com/keejkrej/pi-go/durable/env",
		"github.com/keejkrej/pi-go/durable/storage/jsonl",
		"github.com/keejkrej/pi-go/durable/storage/sqlite",
		"database/sql",
		"modernc.org/sqlite",
		"os",
		"encoding/json",
		"C",
	)

	portableSqliteImports := srbPackageImports(t, filepath.Join("storage", "sqlite"), "node")
	srbAssertNoImports(t, portableSqliteImports,
		"os",
		"os/exec",
		"modernc.org/sqlite",
		"encoding/json",
		"C",
	)

	portableEnvImports := srbPackageImports(t, "env", "node")
	srbAssertNoImports(t, portableEnvImports,
		"os",
		"os/exec",
		"syscall",
		"encoding/json",
		"C",
	)

	portableJsonlImports := srbPackageImports(t, filepath.Join("storage", "jsonl"), "node")
	srbAssertNoImports(t, portableJsonlImports,
		"os",
		"os/exec",
		"database/sql",
		"modernc.org/sqlite",
		"github.com/keejkrej/pi-go/durable/storage/sqlite",
		"encoding/json",
		"C",
	)
}

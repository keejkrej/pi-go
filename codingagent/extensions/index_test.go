// Tests for codingagent/extensions/index.go.

package extensions_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/codingagent/extensions"
)

func TestBuiltInExtensionsBarebone(t *testing.T) {
	if len(extensions.BuiltInExtensions) != 0 {
		t.Fatalf("expected empty BuiltInExtensions in barebone build, got %d", len(extensions.BuiltInExtensions))
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "index.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse index.go imports: %v", err)
	}
	forbidden := []string{"mcpext", "codemodeext", "llamaext", "toolsearchext"}
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		for _, ban := range forbidden {
			if strings.Contains(path, ban) {
				t.Fatalf("index.go must not import %q (found %q)", ban, path)
			}
		}
	}
}

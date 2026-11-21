package diagnosticredact

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/logging/redact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecordKeepsConservativeURLMasking(t *testing.T) {
	redactor := redact.FromEntries(nil)
	const privateURL = "https://user:synthetic/path@host.test/"
	const publicMessage = "ordinary client detail"
	const diagnostic = "Authorization:\n Bearer synthetic-folded\n\n" + publicMessage
	assert.Equal(t, "https://***@host.test/", Record(redactor, privateURL))
	context := strings.Repeat("a", 17*1024)
	raw, err := json.Marshal(map[string]string{
		"msg": diagnostic, "url": privateURL, "level": "error", "context": context,
	})
	require.NoError(t, err)
	var cleaned map[string]string
	require.NoError(t, json.Unmarshal([]byte(Record(redactor, string(raw))), &cleaned))
	assert.Equal(t, "error", cleaned["level"])
	assert.Equal(t, context, cleaned["context"])
	assert.Equal(t, redactor.Mask(), cleaned["url"])
	assert.Equal(t, redactor.Mask(), cleaned["msg"])
}

func TestRecordKeepsCompleteInputMaskingAcrossMixedRecords(t *testing.T) {
	const canary = "synthetic-mixed-record-value"
	const detail = "ordinary mixed record detail"
	input := "\"Authorization:\"\n Bearer " + canary + "\n\n" + detail
	output := Record(redact.FromEntries(nil), input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, detail)
}

func TestRecordKeepsOrdinaryPrefixAtTerminalFold(t *testing.T) {
	const canary = "synthetic-terminal-fold-value"
	const detail = "ordinary detail before terminal fold"
	input := detail + "\nAuthorization: Bearer synthetic-initial-value\n " + canary
	output := Record(redact.FromEntries(nil), input)
	assert.NotContains(t, output, canary)
	assert.Contains(t, output, detail)
}

func TestCoreDiagnosticRedactionBoundary(t *testing.T) {
	root := os.DirFS("../..")
	files := token.NewFileSet()
	for _, dir := range []string{"cmd", "internal", "pkg"} {
		require.NoError(
			t,
			fs.WalkDir(root, dir, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() || skipRedactionBoundaryFile(path) {
					return err
				}
				content, err := fs.ReadFile(root, path)
				if err != nil {
					return err
				}
				file, err := parser.ParseFile(files, path, content, parser.SkipObjectResolution)
				if err != nil {
					return err
				}
				checkRedactionSelectors(t, files, path, file)
				return nil
			}),
		)
	}
}

func skipRedactionBoundaryFile(path string) bool {
	return !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
		path == "internal/diagnosticredact/redact.go"
}

func checkRedactionSelectors(t *testing.T, files *token.FileSet, path string, file *ast.File) {
	t.Helper()
	alias := sharedRedactAlias(file)
	require.NotEqual(t, ".", alias, "%s uses a dot import of the shared redactor", path)
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && bypassesDiagnosticHelper(path, alias, selector) {
			t.Errorf("%s uses %s outside the Core diagnostic redaction helper",
				files.Position(selector.Pos()), selector.Sel.Name)
		}
		return true
	})
}

func sharedRedactAlias(file *ast.File) string {
	for _, imported := range file.Imports {
		if imported.Path.Value == `"github.com/devsy-org/devsy/logging/redact"` {
			if imported.Name != nil {
				return imported.Name.Name
			}
			return "redact"
		}
	}
	return ""
}

func bypassesDiagnosticHelper(path, alias string, selector *ast.SelectorExpr) bool {
	switch selector.Sel.Name {
	case "NewStream", "NewDiagnosticStream":
		packageName, ok := selector.X.(*ast.Ident)
		return ok && packageName.Name == alias
	case "RedactDiagnostic":
		return true
	case "Redact":
		switch path {
		case "cmd/workspace/gpg_tunnel.go", "pkg/log/logger.go", "pkg/subprocess/capture.go":
			return true
		}
	}
	return false
}

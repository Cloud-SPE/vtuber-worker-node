package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// finding is one lint hit. Separate from the on-disk string so tests
// can assert counts and positions without parsing formatted output.
type finding struct {
	File string
	Line int
	Path string
}

func (f finding) format() string {
	return fmt.Sprintf(
		"%s:%d: payment-middleware-check: mux.Register(...) called with capability path %q\n"+
			"  Remediation: paths under /v1/ are paid routes and MUST be registered via RegisterPaidRoute.\n"+
			"  See: docs/design-docs/core-beliefs.md §3, internal/runtime/http/mux.go",
		f.File, f.Line, f.Path,
	)
}

// checkTree walks root and returns every violation found. Skips
// vendor/, .git/, and the lint/ directory itself (which parses
// string-literal test fixtures that would otherwise trip the check).
func checkTree(root string) ([]finding, error) {
	fset := token.NewFileSet()
	var out []finding
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			switch base {
			case "vendor", ".git", "lint":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		hits, ferr := checkFile(fset, path)
		if ferr != nil {
			return ferr
		}
		out = append(out, hits...)
		return nil
	})
	return out, err
}

// checkFile parses one .go file and returns its findings. Exported-
// style enough for tests, lower-case enough to stay package-private.
func checkFile(fset *token.FileSet, path string) ([]finding, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	file, err := parser.ParseFile(fset, path, src, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var out []finding
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// Only the `Register` method — NOT `RegisterPaidRoute`, which
		// is the intended way in and always safe.
		if sel.Sel.Name != "Register" {
			return true
		}
		// Register(method, path, handler) is a 3-arg signature; reject
		// 2-arg forms (like net/http.ServeMux.Handle) that happen to
		// share the method name.
		if len(call.Args) != 3 {
			return true
		}
		pathLit, ok := call.Args[1].(*ast.BasicLit)
		if !ok || pathLit.Kind != token.STRING {
			return true
		}
		pathStr, err := strconv.Unquote(pathLit.Value)
		if err != nil {
			return true
		}
		if !strings.HasPrefix(pathStr, "/v1/") {
			return true
		}
		pos := fset.Position(call.Pos())
		out = append(out, finding{
			File: pos.Filename,
			Line: pos.Line,
			Path: pathStr,
		})
		return true
	})
	return out, nil
}

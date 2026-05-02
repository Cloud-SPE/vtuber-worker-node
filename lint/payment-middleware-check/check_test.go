package main

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeGo(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("writeGo: %v", err)
	}
	return p
}

// TestCheckFile_ViolationSimple is the canonical bad case: a
// .Register() call with a /v1/-prefix literal path.
func TestCheckFile_ViolationSimple(t *testing.T) {
	src := `package x

import "net/http"

type mux struct{}
func (m *mux) Register(method, path string, h http.HandlerFunc) {}

func use() {
	var m mux
	m.Register("POST", "/v1/chat/completions", nil)
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, err := checkFile(token.NewFileSet(), p)
	if err != nil {
		t.Fatalf("checkFile: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("hits: got %d, want 1: %+v", len(hits), hits)
	}
	if hits[0].Path != "/v1/chat/completions" {
		t.Errorf("path: got %q", hits[0].Path)
	}
	if !strings.Contains(hits[0].format(), "RegisterPaidRoute") {
		t.Errorf("format should recommend RegisterPaidRoute; got: %s", hits[0].format())
	}
}

// TestCheckFile_AllowedRegisterPaidRoute: RegisterPaidRoute with a
// /v1/ path should NOT trip the lint (method name differs).
func TestCheckFile_AllowedRegisterPaidRoute(t *testing.T) {
	src := `package x

type mod struct{}
type mux struct{}
func (m *mux) RegisterPaidRoute(_ *mod) {}

func use() {
	var m mux
	m.RegisterPaidRoute(nil)
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, _ := checkFile(token.NewFileSet(), p)
	if len(hits) != 0 {
		t.Errorf("RegisterPaidRoute should be allowed; got hits: %+v", hits)
	}
}

// TestCheckFile_AllowedRegisterUnpaid: Register with an unpaid path
// (/health, /registry/offerings) should be allowed.
func TestCheckFile_AllowedRegisterUnpaid(t *testing.T) {
	src := `package x

import "net/http"

type mux struct{}
func (m *mux) Register(method, path string, h http.HandlerFunc) {}

func use() {
	var m mux
	m.Register("GET", "/health", nil)
	m.Register("GET", "/registry/offerings", nil)
	m.Register("POST", "/v1/payment/ticket-params", nil)
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, _ := checkFile(token.NewFileSet(), p)
	if len(hits) != 0 {
		t.Errorf("unpaid paths should be allowed; got hits: %+v", hits)
	}
}

// TestCheckFile_MultipleViolations: several bad calls in one file.
func TestCheckFile_MultipleViolations(t *testing.T) {
	src := `package x

import "net/http"

type mux struct{}
func (m *mux) Register(method, path string, h http.HandlerFunc) {}

func use() {
	var m mux
	m.Register("POST", "/v1/chat/completions", nil)
	m.Register("POST", "/v1/embeddings", nil)
	m.Register("GET", "/health", nil) // should NOT flag
	m.Register("POST", "/v1/audio/speech", nil)
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, _ := checkFile(token.NewFileSet(), p)
	if len(hits) != 3 {
		t.Fatalf("hits: got %d, want 3: %+v", len(hits), hits)
	}
	wantPaths := map[string]bool{
		"/v1/chat/completions": true,
		"/v1/embeddings":       true,
		"/v1/audio/speech":     true,
	}
	for _, h := range hits {
		if !wantPaths[h.Path] {
			t.Errorf("unexpected path: %q", h.Path)
		}
	}
}

// TestCheckFile_NonLiteralPath: when path is a variable, not a
// literal, we can't inspect its value statically — quietly skip. A
// future improvement could track const-expression paths, but the
// common case is a literal string at the call site.
func TestCheckFile_NonLiteralPath(t *testing.T) {
	src := `package x

import "net/http"

type mux struct{}
func (m *mux) Register(method, path string, h http.HandlerFunc) {}

var chatPath = "/v1/chat/completions"

func use() {
	var m mux
	m.Register("POST", chatPath, nil) // can't statically resolve
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, _ := checkFile(token.NewFileSet(), p)
	if len(hits) != 0 {
		t.Errorf("non-literal path must not flag; track as tech-debt if this becomes a hole. got: %+v", hits)
	}
}

// TestCheckFile_TwoArgRegister: Register method with a non-matching
// signature (e.g. http.ServeMux.HandleFunc uses 2 args). Must not flag.
func TestCheckFile_TwoArgRegister(t *testing.T) {
	src := `package x

type generic struct{}
func (g *generic) Register(pattern string, handler any) {}

func use() {
	var g generic
	g.Register("/v1/arbitrary", nil)
}
`
	p := writeGo(t, t.TempDir(), "a.go", src)
	hits, _ := checkFile(token.NewFileSet(), p)
	if len(hits) != 0 {
		t.Errorf("2-arg Register must not flag; got: %+v", hits)
	}
}

// TestCheckTree_SkipsLintDir: running against a tree that contains a
// lint/ directory with fixtures that would otherwise trip the check
// — the walker MUST skip lint/ so linter self-tests don't re-feed
// themselves.
func TestCheckTree_SkipsLintDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "lint", "x"), 0o700); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o700); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	bad := `package x

import "net/http"

type mux struct{}
func (m *mux) Register(method, path string, h http.HandlerFunc) {}

func use() {
	var m mux
	m.Register("POST", "/v1/foo", nil)
}
`
	writeGo(t, filepath.Join(root, "lint", "x"), "a.go", bad)
	writeGo(t, filepath.Join(root, "internal"), "b.go", bad)

	hits, err := checkTree(root)
	if err != nil {
		t.Fatalf("checkTree: %v", err)
	}
	// Only b.go should flag; lint/ is skipped.
	if len(hits) != 1 {
		t.Fatalf("hits: got %d, want 1 (lint/ must be skipped): %+v", len(hits), hits)
	}
	if !strings.HasSuffix(hits[0].File, filepath.Join("internal", "b.go")) {
		t.Errorf("wrong file: %s", hits[0].File)
	}
}

// TestRealRepoPasses runs checkTree against the actual worker-node
// root. If the real codebase ever trips the lint, this test catches
// it before CI does. The check is run from .. since this test sits
// inside lint/ (which would be skipped), but it's still useful as a
// regression guard: we point the walker at the repo root and assert
// zero findings.
func TestRealRepoPasses(t *testing.T) {
	// Path from lint/payment-middleware-check → repo root: ../..
	hits, err := checkTree("../..")
	if err != nil {
		t.Fatalf("checkTree real repo: %v", err)
	}
	if len(hits) != 0 {
		for _, h := range hits {
			t.Errorf("live violation: %s", h.format())
		}
	}
}

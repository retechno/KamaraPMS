package app

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

var (
	braces     = regexp.MustCompile(`\{[^}]+\}`)
	constPath  = regexp.MustCompile(`const p = "([^"]+)"`)
	routeLit   = regexp.MustCompile(`mux\.Handle\("([A-Z]+) (/[^"]*)"`)
	routePlusP = regexp.MustCompile(`mux\.Handle\("([A-Z]+) "\+p(?:\+"([^"]*)")?`)
	specPath   = regexp.MustCompile(`^  (/[^:]+):\s*$`)
	specMethod = regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
)

// codeRoutes reads every "METHOD path" a module registers (the modules write them as literals or as p + suffix).
func codeRoutes(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("../*/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no http.go files: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		prefix := ""
		if m := constPath.FindStringSubmatch(src); m != nil {
			prefix = m[1]
		}
		for _, m := range routeLit.FindAllStringSubmatch(src, -1) {
			out[m[1]+" "+braces.ReplaceAllString(m[2], "{}")] = f
		}
		for _, m := range routePlusP.FindAllStringSubmatch(src, -1) {
			out[m[1]+" "+braces.ReplaceAllString(prefix+m[2], "{}")] = f
		}
	}
	return out
}

func specRoutes(t *testing.T) map[string]bool {
	t.Helper()
	b, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	var path string
	for _, line := range strings.Split(string(b), "\n") {
		if m := specPath.FindStringSubmatch(line); m != nil {
			path = braces.ReplaceAllString(m[1], "{}")
			continue
		}
		if line != "" && !strings.HasPrefix(line, " ") {
			path = ""
		}
		if m := specMethod.FindStringSubmatch(line); m != nil && path != "" {
			out[strings.ToUpper(m[1])+" "+path] = true
		}
	}
	return out
}

// TestOpenAPIDescribesEveryRoute fails when a route exists in the code without being in api/openapi.yaml, or the
// other way round: the contract and the server must not drift.
func TestOpenAPIDescribesEveryRoute(t *testing.T) {
	code, spec := codeRoutes(t), specRoutes(t)
	var missing, stale []string
	for r, f := range code {
		if !spec[r] {
			missing = append(missing, r+"  ("+filepath.Base(filepath.Dir(f))+")")
		}
	}
	for r := range spec {
		if _, ok := code[r]; !ok {
			stale = append(stale, r)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("routes missing from api/openapi.yaml:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("api/openapi.yaml describes routes the server does not have:\n  %s", strings.Join(stale, "\n  "))
	}
}

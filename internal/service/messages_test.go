package service

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// messageCalls are the functions whose first string argument is a message
// for people: name → index of that argument.
var messageCalls = map[string]int{"invalid": 0, "Invalid": 0, "T": 1, "tr": 1, "New": 0, "syncPhase": 1}

// sourceMessages finds the literal messages passed to messageCalls in the
// Go files of dirs ("errors.New" only, not other New functions).
func sourceMessages(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	found := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, name, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var fn string
				switch x := call.Fun.(type) {
				case *ast.Ident:
					if x.Name == "New" {
						return true
					}
					fn = x.Name
				case *ast.SelectorExpr:
					pkg, ok := x.X.(*ast.Ident)
					if !ok || (x.Sel.Name == "New") != (pkg.Name == "errors") {
						return true
					}
					fn = x.Sel.Name
				}
				i, ok := messageCalls[fn]
				if !ok || len(call.Args) <= i {
					return true
				}
				lit, ok := call.Args[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				found[s] = fset.Position(lit.Pos()).String()
				return true
			})
		}
	}
	return found
}

var verbRE = regexp.MustCompile(`%[-+# 0]*(\d+|\*)?(\.(\d+|\*))?[a-zA-Z%]`)

func verbs(format string) []string {
	var out []string
	for _, v := range verbRE.FindAllString(format, -1) {
		if v != "%%" {
			out = append(out, v)
		}
	}
	return out
}

func TestMessagesTranslated(t *testing.T) {
	for msg, pos := range sourceMessages(t, ".", "../store", "../api") {
		if strings.Trim(verbRE.ReplaceAllString(msg, ""), " “”:;,.()") == "" {
			continue // only arguments, e.g. "%v"
		}
		if _, ok := catalogs["en"][msg]; !ok {
			t.Errorf("%s: no English for %q", pos, msg)
		}
	}
	base := map[string]string{}
	raw, _ := messageFiles.ReadFile("i18n/it.json")
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatal(err)
	}
	for k, v := range base {
		if k != v {
			t.Errorf("it.json: %q has the value %q", k, v)
		}
	}
	for msg, pos := range sourceMessages(t, ".", "../store", "../api") {
		if _, ok := base[msg]; !ok && strings.Trim(verbRE.ReplaceAllString(msg, ""), " “”:;,.()") != "" {
			t.Errorf("%s: %q missing in it.json", pos, msg)
		}
	}
	for it, en := range catalogs["en"] {
		if !slices.Equal(verbs(it), verbs(en)) {
			t.Errorf("verbs differ: %q → %q", it, en)
		}
	}
}

// errorTexts finds the literal texts of fmt.Errorf and errors.New in the
// Go files of dir; with funcs, only inside the functions of those names
// (each must exist, so a rename does not silently drop the check).
func errorTexts(t *testing.T, dir string, funcs ...string) map[string]string {
	t.Helper()
	found := map[string]string{}
	seen := map[string]bool{}
	fset := token.NewFileSet()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		var roots []ast.Node
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && slices.Contains(funcs, fn.Name.Name) {
				seen[fn.Name.Name] = true
				roots = append(roots, fn)
			}
		}
		if len(funcs) == 0 {
			roots = []ast.Node{f}
		}
		for _, root := range roots {
			ast.Inspect(root, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || !(pkg.Name == "fmt" && sel.Sel.Name == "Errorf" || pkg.Name == "errors" && sel.Sel.Name == "New") {
					return true
				}
				lit, ok := call.Args[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				found[s] = fset.Position(lit.Pos()).String()
				return true
			})
		}
	}
	for _, fn := range funcs {
		if !seen[fn] {
			t.Errorf("%s: no function %s", dir, fn)
		}
	}
	return found
}

// A failed synchronisation saves its error in Italian and shows it later
// in the reader's language: the errors of the provider clients and of the
// functions that fetch a catalog or a guide need a translation too.
func TestSyncErrorsTranslated(t *testing.T) {
	found := map[string]string{}
	for _, dir := range []string{"../playlist/xtream", "../playlist/m3u", "../playlist/stalker", "../catalog", "../epg", "../provider"} {
		maps.Copy(found, errorTexts(t, dir))
	}
	maps.Copy(found, errorTexts(t, ".", "fetch", "SyncGuide", "syncGuideSource", "directGet"))
	for msg, pos := range found {
		if strings.Trim(verbRE.ReplaceAllString(msg, ""), " “”:;,.()") == "" {
			continue // only arguments, e.g. "%w"
		}
		for _, lang := range []string{"it", "en"} {
			if _, ok := catalogs[lang][msg]; !ok {
				t.Errorf("%s: %q missing in %s.json", pos, msg, lang)
			}
		}
	}
	cases := map[string]string{
		"playlist: risposta HTTP 884":                "playlist: HTTP answer 884",
		"xtream get_live_streams: risposta HTTP 884": "xtream get_live_streams: HTTP answer 884",
		"credenziali Xtream rifiutate dal provider":  catalogs["en"]["credenziali Xtream rifiutate dal provider"],
		"guida: risposta HTTP 503":                   "guide: HTTP answer 503",
	}
	for in, want := range cases {
		if got := TranslateText("en", in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	// Shown at once (Localize) as when shown later.
	if got := Localize("pl", &ProviderError{Err: fmt.Errorf("playlist: risposta HTTP %d", 884)}); got == "playlist: risposta HTTP 884" {
		t.Errorf("pl: still Italian: %q", got)
	}
}

func TestLocalize(t *testing.T) {
	err := invalid("id non valido")
	if got := Localize("en", err); got != catalogs["en"]["id non valido"] {
		t.Errorf("en: %q", got)
	}
	if got := Localize("it", err); got != "id non valido" {
		t.Errorf("it: %q", got)
	}
	busy := ConnectionBusy{Max: 2}
	if got := Localize("en", busy); !strings.Contains(got, "2") || got == busy.Error() {
		t.Errorf("busy: %q", got)
	}
}

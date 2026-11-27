package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestTranslationCoverageAndFormats(t *testing.T) {
	verbs := regexp.MustCompile(`%\+?(?:\[\d+\])?\d*(?:\.\d+)?[a-zA-Z%]`)
	for source, translated := range EnglishText {
		if translated == "" {
			t.Fatalf("empty translation: %s", source)
		}
		if !slices.Equal(verbs.FindAllString(source, -1), verbs.FindAllString(translated, -1)) {
			t.Fatalf("format arguments differ: %q -> %q", source, translated)
		}
	}

	var files []string
	for _, root := range []string{"../../cmd", "../../internal"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".go") && !strings.HasSuffix(d.Name(), "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	fset := token.NewFileSet()
	for _, file := range files {
		tree, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			isTrCall := false
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				isTrCall = fn.Name == "tr"
			case *ast.SelectorExpr:
				if pkg, ok := fn.X.(*ast.Ident); ok && pkg.Name == "i18n" && fn.Sel.Name == "Tr" {
					isTrCall = true
				}
			}
			if !isTrCall {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			source, _ := strconv.Unquote(lit.Value)
			if _, ok := EnglishText[source]; !ok {
				t.Errorf("%s: missing translation for %q", file, source)
			}
			return true
		})
	}
}

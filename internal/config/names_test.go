package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"testing"
)

// TopLevelNames は、Parse が最上位で読む項目の名前（switch m.name の case）と、同じ並びで一致する。
// case は文字列を直に書く。定数などで書くと名前を読めないので、試験を落とす（足し忘れを見逃さないため。PR #309 の再レビューの推奨 1）。
// ここで確かめるのは Parse との一致までで、ヘルプの語の一覧（FR-148）に出るには、docs/usage.md の「設定ファイルの項目」の表にも書く
// （internal/editor の TestStandardFinderHelp が、すべての名前が一覧に出ることを確かめる）。
func TestTopLevelNames(t *testing.T) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "config.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "Parse" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if sel, ok := sw.Tag.(*ast.SelectorExpr); !ok || sel.Sel.Name != "name" {
				return true
			}
			for _, st := range sw.Body.List {
				for _, e := range st.(*ast.CaseClause).List {
					lit, ok := e.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Errorf("Parse の case %s が文字列の直書きでない", fs.Position(e.Pos()))
						continue
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Errorf("Parse の case %s: %v", lit.Value, err)
					}
					cases = append(cases, s)
				}
			}
			return false
		})
		return false
	})
	if got := TopLevelNames(); len(cases) == 0 || !slices.Equal(got, cases) {
		t.Errorf("TopLevelNames = %v, Parse の case = %v", got, cases)
	}
}

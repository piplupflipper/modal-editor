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
// 項目を足したときに、ヘルプの語の一覧（FR-148）から落ちないようにする。
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
					if lit, ok := e.(*ast.BasicLit); ok {
						s, _ := strconv.Unquote(lit.Value)
						cases = append(cases, s)
					}
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

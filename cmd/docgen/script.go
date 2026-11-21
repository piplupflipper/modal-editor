package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/traefik/yaegi/stdlib"
	"github.com/traefik/yaegi/stdlib/unrestricted"
)

// scriptPackages は、スクリプトの拡張機能（cmd/vi-imitation-ext-script）が yaegi に渡す標準ライブラリの記号の表を、
// import のパスから記号の名前の一覧への対応にする。
// 表は stdlib.Symbols と unrestricted.Symbols で、cmd/vi-imitation-ext-script の newInterpreter が Use するものと同じである。
// 表のキーは yaegi の決まりで「import のパス/パッケージ名」である。
// 記号の名前のうち _ で始まるもの（yaegi がインターフェースを包む型）は、スクリプトから書かないので除く。
func scriptPackages() map[string][]string {
	out := map[string][]string{}
	for _, table := range []map[string]map[string]reflect.Value{stdlib.Symbols, unrestricted.Symbols} {
		for key, syms := range table {
			i := strings.LastIndex(key, "/")
			if i < 0 {
				continue // パッケージでない項目（yaegi の内部で使う表）
			}
			path := key[:i]
			if _, ok := out[path]; !ok {
				out[path] = []string{} // 記号の無いパッケージ（型の引数を取る関数だけの slices など）も、import できるので並べる
			}
			for name := range syms {
				if !strings.HasPrefix(name, "_") && !slices.Contains(out[path], name) {
					out[path] = append(out[path], name)
				}
			}
		}
	}
	for _, names := range out {
		slices.Sort(names)
	}
	return out
}

// writeScriptPackages は、スクリプトが import できるパッケージの import のパスを並べる。
func writeScriptPackages(w *bytes.Buffer) {
	pkgs := scriptPackages()
	paths := make([]string, 0, len(pkgs))
	for p := range pkgs {
		paths = append(paths, code(p))
	}
	slices.Sort(paths)
	fmt.Fprintln(w, strings.Join(paths, "、"))
}

// writeScriptSymbols は、スクリプトが import できる標準ライブラリのパッケージごとに、使える記号の名前を並べる。
// 型の引数を取る関数と型（slices.Sort など）は、yaegi の記号の表に入らないので、ここに無い。
func writeScriptSymbols(w *bytes.Buffer) {
	pkgs := scriptPackages()
	paths := make([]string, 0, len(pkgs))
	for p := range pkgs {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for i, p := range paths {
		if i > 0 {
			fmt.Fprintln(w)
		}
		names := strings.Join(pkgs[p], " ")
		if names == "" {
			names = "（使える記号は無い）"
		}
		fmt.Fprintf(w, "### %s\n\n%s\n", p, names)
	}
}

// writeServerAPI は、ext/server の公開の関数、変数、型、メソッドを、宣言の形と doc コメントの最初の段落で書く。
// ソースのディレクトリ dir（ext/server）を go/doc で読む。並びは go/doc の並び（名前の順）である。
func writeServerAPI(w *bytes.Buffer, api *extAPI, dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return err
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		f, perr := parser.ParseFile(fset, p, src, parser.ParseComments)
		if perr != nil {
			return perr
		}
		files = append(files, f)
	}
	pkg, err := doc.NewFromFiles(fset, files, "github.com/piplupflipper/vi-imitation/ext/server")
	if err != nil {
		return err
	}
	decl := func(n any) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n) // 構文木を書くだけなので失敗しない
		return joinLines(b.String())
	}
	sig := func(f *doc.Func) string {
		return decl(&ast.FuncDecl{Recv: f.Decl.Recv, Name: f.Decl.Name, Type: f.Decl.Type})
	}
	// first は、doc コメントの最初の段落を、先頭の「名前 は、」を除いて、配布物の利用者が読める形（extAPI.text）にする。
	first := func(s, name string) string {
		p, _, _ := strings.Cut(strings.TrimSpace(s), "\n\n")
		return api.text(dropSubject(p, name))
	}
	values := func(vs []*doc.Value) {
		for _, v := range vs {
			for _, s := range v.Decl.Specs {
				vs := s.(*ast.ValueSpec)
				for _, n := range vs.Names {
					if n.IsExported() {
						d := commentText(vs.Doc)
						if d == "" {
							d = v.Doc
						}
						writeItem(w, code(n.Name), first(d, n.Name))
					}
				}
			}
		}
	}
	fmt.Fprintln(w, "#### 関数と変数")
	fmt.Fprintln(w)
	for _, f := range pkg.Funcs {
		writeItem(w, code(sig(f)), first(f.Doc, f.Name))
	}
	values(pkg.Consts)
	values(pkg.Vars)
	for _, t := range pkg.Types {
		fmt.Fprintf(w, "\n#### `server.%s`\n\n", t.Name)
		if d := first(t.Doc, t.Name); d != "" {
			fmt.Fprintf(w, "%s\n\n", d)
		}
		spec := t.Decl.Specs[0].(*ast.TypeSpec)
		if st, ok := spec.Type.(*ast.StructType); ok {
			for _, f := range st.Fields.List {
				for _, n := range f.Names {
					if n.IsExported() {
						writeItem(w, code(n.Name+" "+decl(f.Type)), first(commentText(f.Doc), n.Name))
					}
				}
			}
		} else {
			fmt.Fprintf(w, "`type %s %s`\n\n", t.Name, decl(spec.Type))
		}
		values(t.Consts)
		values(t.Vars)
		for _, f := range t.Funcs {
			writeItem(w, code(sig(f)), first(f.Doc, f.Name))
		}
		for _, f := range t.Methods {
			writeItem(w, code(sig(f)), first(f.Doc, f.Name))
		}
	}
	// 最後の型の後の空行を 1 つにそろえる（印の終わりの直前に空行を残さない）
	trimmed := bytes.TrimRight(w.Bytes(), "\n")
	w.Truncate(len(trimmed))
	w.WriteByte('\n')
	return nil
}

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// extAPI は、拡張機能 API の公開パッケージ ext のソースから読んだ、型、定数、メソッドである。
// 型を Go の reflect で読まずにソースを読むのは、doc コメント（項目の説明）を文書に写すためである。
type extAPI struct {
	types  map[string]*typeInfo
	consts []constInfo // ファイル名の順、ファイルの中の宣言の順
	// hidden は、公開していない定数の名前から doc コメントへの対応である。
	// 公開している型の doc が JSON の形の説明をそこへ譲っているとき（DecorationSetParams の decorationFields）に写す。
	hidden map[string]string
}

// typeInfo は、ext の 1 つの型の宣言である。
type typeInfo struct {
	name string
	doc  string
	expr ast.Expr
	// customJSON は、型が MarshalJSON か UnmarshalJSON を持つ（JSON の形が項目の並びと違う）ことを表す。
	customJSON bool
}

// constInfo は、ext の 1 つの定数の宣言である。
type constInfo struct {
	name  string
	typ   string // 型の名前。型を書かない定数では空
	value string // 文字列の定数は引用符を外した値、数の定数は 10 進の表記
	doc   string
}

// methodInfo は、拡張機能 API の 1 つのメソッド（ext の Method で始まる定数）である。
type methodInfo struct {
	constInfo
	fromHost bool // 本体から拡張機能へ送るか（偽なら拡張機能から本体へ）
	request  bool // リクエストか（偽なら通知）
	params   string
	result   string
	notes    []string // doc のうち、向き、種類、params、result の文を除いた文
}

// parseExtAPI は、ディレクトリ dir（ext）のテストでない Go のソースを読む。
func parseExtAPI(dir string) (*extAPI, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	api := &extAPI{types: map[string]*typeInfo{}, hidden: map[string]string{}}
	fset := token.NewFileSet()
	var methods []*ast.FuncDecl
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, p, src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				methods = append(methods, d)
			case *ast.GenDecl:
				if err := api.addDecl(d); err != nil {
					return nil, fmt.Errorf("%s: %w", fset.Position(d.Pos()), err)
				}
			}
		}
	}
	for _, m := range methods {
		if m.Recv == nil || (m.Name.Name != "MarshalJSON" && m.Name.Name != "UnmarshalJSON") {
			continue
		}
		recv := m.Recv.List[0].Type
		if s, ok := recv.(*ast.StarExpr); ok {
			recv = s.X
		}
		if id, ok := recv.(*ast.Ident); ok && api.types[id.Name] != nil {
			api.types[id.Name].customJSON = true
		}
	}
	return api, nil
}

// addDecl は、型と定数の宣言を読む。公開していない名前は読まない。
func (api *extAPI) addDecl(d *ast.GenDecl) error {
	for _, s := range d.Specs {
		doc := commentText(specDoc(d, s))
		switch s := s.(type) {
		case *ast.TypeSpec:
			if s.Name.IsExported() {
				api.types[s.Name.Name] = &typeInfo{name: s.Name.Name, doc: doc, expr: s.Type}
			}
		case *ast.ValueSpec:
			if d.Tok != token.CONST {
				continue
			}
			typ := ""
			if id, ok := s.Type.(*ast.Ident); ok {
				typ = id.Name
			}
			for i, n := range s.Names {
				if !n.IsExported() {
					// 公開していない定数の doc は、JSON の形の説明に使う（decorationFields。writeType）
					api.hidden[n.Name] = doc
					continue
				}
				if i >= len(s.Values) {
					continue
				}
				lit, ok := s.Values[i].(*ast.BasicLit)
				if !ok {
					// 単項の - の付いた数（ErrorCodeReadOnly）
					if u, uok := s.Values[i].(*ast.UnaryExpr); uok {
						if l, lok := u.X.(*ast.BasicLit); lok {
							lit = &ast.BasicLit{Kind: l.Kind, Value: u.Op.String() + l.Value}
							ok = true
						}
					}
				}
				if !ok {
					return fmt.Errorf("定数 %s の値が文字列か数のリテラルでない", n.Name)
				}
				value := lit.Value
				if lit.Kind == token.STRING {
					v, err := strconv.Unquote(lit.Value)
					if err != nil {
						return err
					}
					value = v
				}
				api.consts = append(api.consts, constInfo{name: n.Name, typ: typ, value: value, doc: doc})
			}
		}
	}
	return nil
}

// specDoc は、宣言の 1 つの項目の doc コメントを返す。項目に無く、宣言が 1 つの項目だけなら、宣言の doc コメントを返す。
func specDoc(d *ast.GenDecl, s ast.Spec) *ast.CommentGroup {
	var doc *ast.CommentGroup
	switch s := s.(type) {
	case *ast.TypeSpec:
		doc = s.Doc
	case *ast.ValueSpec:
		doc = s.Doc
	}
	if doc == nil && len(d.Specs) == 1 {
		doc = d.Doc
	}
	return doc
}

// commentText は、doc コメントの本文を返す。末尾の空行と、req: の注釈の行は除く。
func commentText(c *ast.CommentGroup) string {
	if c == nil {
		return ""
	}
	var lines []string
	for _, l := range strings.Split(c.Text(), "\n") {
		if strings.HasPrefix(l, "req: ") {
			continue
		}
		lines = append(lines, l)
	}
	return dropRefs(strings.TrimSpace(strings.Join(lines, "\n")))
}

// parenRE は、入れ子でない全角の括弧の組である。
var parenRE = regexp.MustCompile(`（[^（）]*）`)

// refRE は、リポジトリの中の文書と issue と PR への参照で始まる文の断片である（基本設計 5.1、FR-94、#9 の裁定、PR #249 のレビュー）。
var refRE = regexp.MustCompile(`^(基本設計|要件定義|FR-|NFR-|#\d|PR #)`)

// dropRefs は、doc コメントの括弧の中の、設計の文書と issue と PR への参照を除く。
// 配布物だけを持つ利用者はそれらを読めず、文書のトークンを減らすためである。
// 括弧の中を「。」で分け、参照で始まる断片を除き、残りが無ければ括弧ごと除く（「（名前は仮。基本設計 5.1）」は「（名前は仮）」）。
func dropRefs(doc string) string {
	return parenRE.ReplaceAllStringFunc(doc, func(p string) string {
		inner := strings.TrimSuffix(strings.TrimPrefix(p, "（"), "）")
		var keep []string
		for _, part := range strings.Split(inner, "。") {
			if !refRE.MatchString(strings.TrimSpace(part)) {
				keep = append(keep, part)
			}
		}
		if len(keep) == 0 {
			return ""
		}
		return "（" + strings.Join(keep, "。") + "）"
	})
}

// constant は、名前が name の定数を返す。無ければ nil を返す。
func (api *extAPI) constant(name string) *constInfo {
	for i := range api.consts {
		if api.consts[i].name == name {
			return &api.consts[i]
		}
	}
	return nil
}

// writeVersion は、拡張機能 API の版（ext.APIVersion）を書く。
func (api *extAPI) writeVersion(w *bytes.Buffer) {
	v := "（不明）"
	if c := api.constant("APIVersion"); c != nil {
		v = code(c.value)
	}
	fmt.Fprintf(w, "この本体の拡張機能 API の版は %s である（`initialize` の `apiVersion`）。\n", v)
}

// writeEnv は、本体が拡張機能の子プロセスに渡す環境変数（ext の Env で始まる定数）を書く。
func (api *extAPI) writeEnv(w *bytes.Buffer) {
	for _, c := range api.consts {
		if strings.HasPrefix(c.name, "Env") {
			writeItem(w, code(c.value), dropSubject(c.doc, c.name))
		}
	}
}

// writeOperations は、拡張機能が provides で引き受けられる本体の操作（ext.Operation の定数）の名前を書く。
func (api *extAPI) writeOperations(w *bytes.Buffer) {
	var names []string
	for _, c := range api.consts {
		if c.typ == "Operation" {
			names = append(names, code(":"+c.value))
		}
	}
	fmt.Fprintln(w, strings.Join(names, "、"))
}

// writeManifest は、宣言ファイルの型（ext.Manifest）と、そこから辿れる型を書く。
func (api *extAPI) writeManifest(w *bytes.Buffer) {
	for _, name := range api.reachable([]string{"Manifest"}, nil) {
		api.writeType(w, name)
	}
}

// methods は、ext の Method で始まる定数を、doc コメントから向き、種類、params、result を読んでメソッドにする。
// 並びは、本体から拡張機能へのものを先に、それぞれメソッドの名前の順にする。
// doc コメントの最初の文から向きと種類を読めなければ誤りにする（メソッドを足すときは、既存のメソッドと同じ書き方にする）。
func (api *extAPI) methods() ([]methodInfo, error) {
	paramsRE := regexp.MustCompile(`params は ([A-Z]\w*)`)
	resultRE := regexp.MustCompile(`result は ([A-Z]\w*)`)
	var out []methodInfo
	for _, c := range api.consts {
		if !strings.HasPrefix(c.name, "Method") {
			continue
		}
		first, _, _ := strings.Cut(c.doc, "。")
		m := methodInfo{constInfo: c}
		switch {
		case strings.Contains(first, "本体から拡張機能"):
			m.fromHost = true
		case strings.Contains(first, "拡張機能から本体"):
		default:
			return nil, fmt.Errorf("%s の doc の最初の文に、向き（本体から拡張機能、拡張機能から本体）が無い", c.name)
		}
		switch {
		case strings.Contains(first, "リクエスト"):
			m.request = true
		case strings.Contains(first, "通知"):
		default:
			return nil, fmt.Errorf("%s の doc の最初の文に、種類（リクエスト、通知）が無い", c.name)
		}
		if p := paramsRE.FindStringSubmatch(c.doc); p != nil {
			m.params = p[1]
		}
		if r := resultRE.FindStringSubmatch(c.doc); r != nil {
			m.result = r[1]
		}
		for _, s := range sentences(c.doc) {
			if strings.HasPrefix(s, c.name+" は") || strings.HasPrefix(s, "params は") || strings.HasPrefix(s, "result は") {
				continue
			}
			m.notes = append(m.notes, s)
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].fromHost != out[j].fromHost {
			return out[i].fromHost
		}
		return out[i].value < out[j].value
	})
	return out, nil
}

// sentences は、doc コメントを文（。で終わる）に分ける。改行は文の区切りにしない（行をまたぐ文は 1 つにつなぐ）。
func sentences(doc string) []string {
	var out []string
	for _, s := range strings.SplitAfter(joinLines(doc), "。") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// joinLines は、doc コメントの行を 1 行につなぐ。日本語の行の間には何も挟まず、ASCII で終わる行と次の行の間には空白を挟む。
func joinLines(doc string) string {
	var sb strings.Builder
	for _, l := range strings.Split(doc, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if s := sb.String(); s != "" && s[len(s)-1] < 0x80 {
			sb.WriteByte(' ')
		}
		sb.WriteString(l)
	}
	return sb.String()
}

// writeMethods は、メソッドの一覧を書く。
func (api *extAPI) writeMethods(w *bytes.Buffer) error {
	ms, err := api.methods()
	if err != nil {
		return err
	}
	for i, m := range ms {
		if i == 0 || ms[i-1].fromHost != m.fromHost {
			if i > 0 {
				fmt.Fprintln(w)
			}
			if m.fromHost {
				fmt.Fprintln(w, "#### 本体から拡張機能へ")
			} else {
				fmt.Fprintln(w, "#### 拡張機能から本体へ")
			}
			fmt.Fprintln(w)
		}
		kind := "通知"
		if m.request {
			kind = "リクエスト"
		}
		var parts []string
		if m.params != "" {
			parts = append(parts, "params は "+code(m.params))
		}
		if m.result != "" {
			parts = append(parts, "result は "+code(m.result))
		}
		head := kind
		if len(parts) > 0 {
			head += "。" + strings.Join(parts, "、")
		}
		writeItem(w, code(m.value), head+"。"+strings.Join(m.notes, ""))
	}
	return nil
}

// writeMessageTypes は、メソッドの params と result の型と、そこから辿れる型のうち、宣言ファイルの型（writeManifest）に無いものを書く。
// 最後に、型の無い定数（ErrorCodeReadOnly など）を書く。
func (api *extAPI) writeMessageTypes(w *bytes.Buffer) error {
	ms, err := api.methods()
	if err != nil {
		return err
	}
	var roots []string
	for _, m := range ms {
		for _, t := range []string{m.params, m.result} {
			if t == "" {
				continue
			}
			if api.types[t] == nil {
				return fmt.Errorf("%s の doc の型 %s が ext に無い", m.name, t)
			}
			roots = append(roots, t)
		}
	}
	skip := map[string]bool{}
	for _, n := range api.reachable([]string{"Manifest"}, nil) {
		skip[n] = true
	}
	for _, name := range api.reachable(roots, skip) {
		api.writeType(w, name)
	}
	var others []constInfo
	for _, c := range api.consts {
		if strings.HasPrefix(c.name, "Method") || strings.HasPrefix(c.name, "Env") || c.name == "APIVersion" || api.types[c.typ] != nil {
			continue
		}
		others = append(others, c)
	}
	if len(others) > 0 {
		fmt.Fprintln(w, "#### その他の定数")
		fmt.Fprintln(w)
		for _, c := range others {
			writeItem(w, code(c.value)+"（"+c.name+"）", c.doc)
		}
	}
	return nil
}

// reachable は、型 roots から、項目の型を辿って届く ext の型の名前を、見つけた順に返す。skip の型は返さず、その先も辿らない。
func (api *extAPI) reachable(roots []string, skip map[string]bool) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(e ast.Expr)
	add := func(name string) {
		t := api.types[name]
		if t == nil || seen[name] || skip[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
		visit(t.expr)
	}
	visit = func(e ast.Expr) {
		switch e := e.(type) {
		case *ast.Ident:
			add(e.Name)
		case *ast.StarExpr:
			visit(e.X)
		case *ast.ArrayType:
			visit(e.Elt)
		case *ast.MapType:
			visit(e.Key)
			visit(e.Value)
		case *ast.StructType:
			for _, f := range e.Fields.List {
				visit(f.Type)
			}
		}
	}
	for _, r := range roots {
		add(r)
	}
	return out
}

// writeType は、型 name の見出し、doc コメント、項目（構造体なら JSON の名前ごと）か定数（文字列などの型なら値ごと）を書く。
func (api *extAPI) writeType(w *bytes.Buffer, name string) {
	t := api.types[name]
	st, isStruct := t.expr.(*ast.StructType)
	if isStruct {
		fmt.Fprintf(w, "#### `%s`\n\n", name)
	} else {
		fmt.Fprintf(w, "#### `%s`（`%s`）\n\n", name, types.ExprString(t.expr))
	}
	if doc := dropSubject(t.doc, name); doc != "" {
		fmt.Fprintln(w, doc)
		fmt.Fprintln(w)
	}
	if t.customJSON {
		fmt.Fprintln(w, "JSON の形は、下の項目の並びではなく、この説明のとおりである（MarshalJSON か UnmarshalJSON を持つ）。")
		fmt.Fprintln(w)
		names := make([]string, 0, len(api.hidden))
		for n := range api.hidden {
			names = append(names, n)
		}
		slices.Sort(names)
		for _, n := range names {
			if api.hidden[n] != "" && regexp.MustCompile(`\b`+n+`\b`).MatchString(t.doc) {
				fmt.Fprintf(w, "JSON の形（%s）:\n\n%s\n\n", n, api.hidden[n])
			}
		}
	}
	if isStruct {
		n := 0
		for _, f := range st.Fields.List {
			n += api.writeField(w, f)
		}
		if n == 0 {
			fmt.Fprintln(w, "項目は無い（`{}`）。")
		}
	} else {
		for _, c := range api.consts {
			if c.typ == name {
				writeItem(w, code(strconv.Quote(c.value))+"（"+c.name+"）", dropSubject(c.doc, c.name))
			}
		}
	}
	fmt.Fprintln(w)
}

// writeField は、構造体の項目 f を、JSON の名前、型、省けるか、doc コメントの 1 項目にして書き、書いた項目の数を返す。
// 公開していない項目と、JSON に出さない項目（json:"-"）は書かない。
func (api *extAPI) writeField(w *bytes.Buffer, f *ast.Field) int {
	tag := ""
	if f.Tag != nil {
		if s, err := strconv.Unquote(f.Tag.Value); err == nil {
			tag = reflect.StructTag(s).Get("json")
		}
	}
	jsonName, opts, _ := strings.Cut(tag, ",")
	if jsonName == "-" && opts == "" {
		return 0
	}
	typ := types.ExprString(f.Type)
	doc := commentText(f.Doc)
	if len(f.Names) == 0 {
		writeItem(w, "（埋め込み "+code(typ)+"）", "この型の項目を同じ階層に並べる。"+joinLines(doc))
		return 1
	}
	n := 0
	for _, name := range f.Names {
		if !name.IsExported() {
			continue
		}
		jn := jsonName
		if jn == "" {
			jn = name.Name
		}
		attr := code(typ)
		if strings.Contains(opts, "omitempty") {
			attr += "、省ける"
		}
		writeItem(w, code(jn)+"（"+attr+"）", dropSubject(doc, name.Name))
		n++
	}
	return n
}

// dropSubject は、doc コメントの先頭の「名前 は、」を除く（「Foo は、バッファの…である。」を「バッファの…である。」にする）。
func dropSubject(doc, name string) string {
	if rest, ok := strings.CutPrefix(doc, name+" は、"); ok {
		return rest
	}
	return doc
}

// writeItem は、箇条書きの 1 項目を書く。doc の 2 行目からは、項目の続きとして字下げする。
func writeItem(w *bytes.Buffer, head, doc string) {
	if doc == "" {
		fmt.Fprintf(w, "- %s\n", head)
		return
	}
	lines := strings.Split(doc, "\n")
	fmt.Fprintf(w, "- %s: %s\n", head, lines[0])
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		fmt.Fprintf(w, "  %s\n", l)
	}
}

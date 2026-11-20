package main

import (
	"embed"
	"fmt"
	"strings"
	"unsafe"

	sitter "github.com/tree-sitter/go-tree-sitter"
	sittergo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	sitterjson "github.com/tree-sitter/tree-sitter-json/bindings/go"
)

// queryFS は、各文法の公式のハイライトのクエリ（highlights.scm）と、このリポジトリで書いたテキストオブジェクトのクエリ（textobjects.scm）である。
//
//go:embed queries/*/*.scm
var queryFS embed.FS

// languageSpec は、1 つの言語の文法とクエリの組と、それを使うファイルの種類である。
type languageSpec struct {
	// name は、言語の名前である。誤りの文言に使う。
	name string
	// fileTypes は、この言語の文法とクエリを使うファイルの種類の一覧で、buffer/open の fileType と、宣言ファイルの filetype の契機の pattern である。
	// 1 つの言語を複数の種類に使える（JSON の文法を "json" と "jsonc" に使う）。1 つの種類は 1 つの言語にだけ書く。
	fileTypes []string
	// language は、文法の TSLanguage へのポインターを返す。
	language func() unsafe.Pointer
	// query は、queryFS の中のハイライトのクエリのパスである。
	query string
	// objects は、queryFS の中のテキストオブジェクトのクエリのパスである。空なら、この言語はテキストオブジェクトを持たない。
	objects string
}

// languages は、対応する言語の一覧である。
// 言語を足すときは、ここと宣言ファイル（manifest.json）の activation と queries/ の下のクエリを足す。
// JSON は、関数、型、引数に当たる構文を持たないので、テキストオブジェクトを持たない。
// JSON の文法（tree-sitter-json）は、コメントを文法に含み（extras）、ハイライトのクエリはコメントにも色を付けるので、"jsonc" にも使う。
// 末尾のカンマは文法に無く、構文木に誤りのノードができるが、パーサーが立ち直るので、後ろの要素にも色が付く（TestDecorationsJSONC）。
var languages = []languageSpec{
	{name: "go", fileTypes: []string{"go"}, language: sittergo.Language, query: "queries/go/highlights.scm", objects: "queries/go/textobjects.scm"},
	{name: "json", fileTypes: []string{"json", "jsonc"}, language: sitterjson.Language, query: "queries/json/highlights.scm"},
}

// language は、1 つの言語の解析の道具をまとめたものである。
// 構文木はバッファごとに持つが、パーサーとクエリは同じ言語のバッファで共有する。ファイルの種類が違っても、言語が同じなら共有する。
// parser は同時に 1 つの解析にしか使えないので、解析の goroutine（highlighter.work）からだけ使う。
// テキストオブジェクトの問い合わせは、別のパーサー（textObjects.parsers）を使う。
// クエリは作った後に変えず、使うたびに QueryCursor を作るので、query は解析の goroutine が、objects は問い合わせの goroutine が使う。
type language struct {
	name    string
	grammar *sitter.Language
	parser  *sitter.Parser
	query   *sitter.Query
	// styles は、クエリのキャプチャの番号から、色の種類への対応である。色を付けないキャプチャは noStyle である。
	styles []styleID
	// objects は、テキストオブジェクトのクエリである。持たない言語では nil である。
	objects *sitter.Query
	// objectCaptures は、objects のキャプチャの番号から、対象と部分への対応である。
	objectCaptures []objectCapture
}

// newLanguage は、spec の文法でパーサーを作り、ハイライトとテキストオブジェクトのクエリを解釈する。
func newLanguage(spec languageSpec) (*language, error) {
	src, err := queryFS.ReadFile(spec.query)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", spec.name, err)
	}
	lang := sitter.NewLanguage(spec.language())
	parser := sitter.NewParser()
	if err = parser.SetLanguage(lang); err != nil {
		parser.Close()
		return nil, fmt.Errorf("%s: %w", spec.name, err)
	}
	query, qerr := sitter.NewQuery(lang, string(src))
	if qerr != nil {
		parser.Close()
		return nil, fmt.Errorf("%s: %s: %s", spec.name, spec.query, qerr.Error())
	}
	names := query.CaptureNames()
	styles := make([]styleID, len(names))
	for i, n := range names {
		styles[i] = styleFor(n)
	}
	l := &language{name: spec.name, grammar: lang, parser: parser, query: query, styles: styles}
	if spec.objects == "" {
		return l, nil
	}
	src, err = queryFS.ReadFile(spec.objects)
	if err != nil {
		l.close()
		return nil, fmt.Errorf("%s: %w", spec.name, err)
	}
	objects, qerr := sitter.NewQuery(lang, string(src))
	if qerr != nil {
		l.close()
		return nil, fmt.Errorf("%s: %s: %s", spec.name, spec.objects, qerr.Error())
	}
	l.objects = objects
	for _, n := range objects.CaptureNames() {
		l.objectCaptures = append(l.objectCaptures, parseObjectCapture(n))
	}
	return l, nil
}

// close は、パーサーとクエリの C のメモリを解放する。
func (l *language) close() {
	if l.objects != nil {
		l.objects.Close()
	}
	l.query.Close()
	l.parser.Close()
}

// styleID は、decoration/set で送る色の種類（基本設計 6.1 の表）の番号である。
// 全文の 1 バイトごとに色を記録するので、名前の文字列ではなく 1 バイトの番号で持つ。
type styleID uint8

// noStyle は、色を付けないことを表す。
const noStyle styleID = 0

// styleNames は、styleID から色の種類の名前への対応である。0 番は noStyle で、名前は空である。
var styleNames = []string{"", "comment", "keyword", "string", "number", "constant", "function", "type", "variable", "operator", "punctuation"}

// String は、色の種類の名前を返す。
func (s styleID) String() string { return styleNames[s] }

// styleByName は、色の種類の名前から styleID を引く。
func styleByName(name string) (styleID, bool) {
	for i, n := range styleNames {
		if i > 0 && n == name {
			return styleID(i), true
		}
	}
	return noStyle, false
}

// captureAliases は、6.1 の表に同じ名前が無いキャプチャ名のうち、表の名前に写すものである。
var captureAliases = map[string]string{
	// 文字列の中のエスケープ（"\n" の \n）は、文字列のリテラルの一部として色を付ける。
	"escape": "string",
}

// styleFor は、ハイライトのクエリのキャプチャ名を、6.1 の表の色の種類の名前に写す。
//
// キャプチャ名は "function.method" のようにドットで細分されている（tree-sitter の慣習）。
// 名前そのものが表か captureAliases にあればそれを返し、無ければ最後のドットから後ろを除いて探し直す。
// "function.method" と "function.builtin" は "function" に、"constant.builtin" は "constant" に、"string.special.key" は "string" になる。
// どこまで除いても見つからない名前（Go の "property" など）は noStyle を返し、その範囲には色を付けない。
func styleFor(capture string) styleID {
	name := capture
	for {
		if alias, ok := captureAliases[name]; ok {
			name = alias
		}
		if id, ok := styleByName(name); ok {
			return id
		}
		i := strings.LastIndexByte(name, '.')
		if i < 0 {
			return noStyle
		}
		name = name[:i]
	}
}

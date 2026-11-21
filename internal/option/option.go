// Package option は、設定の項目の表（名前、型、効く範囲、既定値）と、項目の値を持つ（基本設計 7.3。FR-131）。
//
// 基本設計 2 章の土台の層に置き、他のパッケージを import しない。
// 設定ファイルを読む internal/config と、Ex コマンド（:option.set、:option.get、:set）を解析する internal/command/ex は、
// どちらも機能の層で互いに import できないので、両方が引く表をこの層に置く。
// 値を持つのは internal/editor である。範囲がエディタ全体の項目の値は Editor に 1 つ、バッファごとの項目の値はバッファごとに 1 つの Values に持つ。
//
// # 名前
//
// 正規の名前は、点で区切った camelCase で、何の設定かが名前で分かるものにする（lineNumber.show など）。
// Vim の短い名前（number）は正規の名前にせず、:set の別名の層（internal/command/ex）でだけ受け付ける。
// 設定ファイルでは、点で区切った名前を入れ子のキーとして書く（"lineNumber": { "show": true }）。
//
// # 値の使い方
//
// 範囲がエディタ全体の項目は、使う側（描画など）が使うたびに今の値を読む。値が変わったときに、その項目のためだけの処理は呼ばない。
// 範囲がバッファごとの項目を変えたときは、項目の名前によらず、拡張機能へバッファの属性を知らせ直す（internal/editor）。
// 範囲がバッファごとの項目も、使う側（インサートモードの <Tab>、描画など）が使うたびにそのバッファの今の値を読む。
//
// # バッファごとの項目の初めの値
//
// バッファを開くときに、範囲がバッファごとの項目（Def.Detected のものを除く）の初めの値を、次の段を順に当てて決める。
// 後に当てた段の値が勝つ（#318 のオーナーの決め。基本設計 7.3。FR-163、FR-164）。
//
//  1. 表の既定値（Def.Default）
//  2. 設定ファイルの最上位に書いた値
//  3. 本体のファイルの種類ごとの既定（FileTypeDefaults）
//  4. 設定ファイルのファイルの種類ごとの値（fileTypeOptions）
//  5. 開いたファイルに当たる .editorconfig の値
//
// 3 を 2 の後に置くのは、Neovim が利用者の設定（init.lua の :set）の後にファイルの種類ごとの ftplugin を当てるのと同じにするためである。
// 段を当てるのは internal/editor で、このパッケージは 1 と 3 の表を持つ。
package option

import (
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
)

// Type は、項目の値の型である。
type Type int

// 値の型。
const (
	// Bool は真偽で、文字列では true と false で書く。
	Bool Type = iota + 1
	// Int は整数で、文字列では 10 進で書く（先頭の - を許す）。
	Int
	// String は文字列である。
	String
)

// String は、型の名前（"bool"、"int"、"string"）を返す。
func (t Type) String() string {
	switch t {
	case Bool:
		return "bool"
	case Int:
		return "int"
	case String:
		return "string"
	}
	return "Type(" + strconv.Itoa(int(t)) + ")"
}

// Scope は、項目の効く範囲である。
type Scope int

// 効く範囲。
const (
	// ScopeEditor は、エディタ全体に 1 つの値を持つ項目である。設定ファイルで初めの値を書ける。
	ScopeEditor Scope = iota + 1
	// ScopeBuffer は、バッファごとに値を持つ項目である。初めの値はバッファを開くときに、パッケージの doc の段を順に当てて決まる。
	// 設定ファイルの最上位に書いた値は、バッファを開くときの初めの値（2 の段）になる（FR-163）。
	// ただし Def.Detected の項目は、バッファを開くときにファイルから判定して決め、設定ファイルには書けない。
	ScopeBuffer
)

// Value は、項目の 1 つの値である。ゼロ値は、どの型でもない値（Valid が false）である。
type Value struct {
	typ Type
	b   bool
	n   int
	s   string
}

// BoolValue は、真偽の値を返す。
func BoolValue(b bool) Value { return Value{typ: Bool, b: b} }

// IntValue は、整数の値を返す。
func IntValue(n int) Value { return Value{typ: Int, n: n} }

// StringValue は、文字列の値を返す。
func StringValue(s string) Value { return Value{typ: String, s: s} }

// Type は、値の型を返す。ゼロ値では 0 である。
func (v Value) Type() Type { return v.typ }

// Valid は、値がどれかの型の値であるかを返す。
func (v Value) Valid() bool { return v.typ != 0 }

// Bool は、真偽の値を返す。真偽でない値では false である。
func (v Value) Bool() bool { return v.b }

// Int は、整数の値を返す。整数でない値では 0 である。
func (v Value) Int() int { return v.n }

// String は、値を文字列で返す。真偽は true か false、整数は 10 進、文字列はそのままである。
// Def.Parse で読み戻すと同じ値になる。:option.get が出す値もこの形である。
func (v Value) String() string {
	switch v.typ {
	case Bool:
		return strconv.FormatBool(v.b)
	case Int:
		return strconv.Itoa(v.n)
	case String:
		return v.s
	}
	return ""
}

// Def は、設定の表の 1 行である。
type Def struct {
	// Name は、正規の名前である。
	Name string
	// Type は、値の型である。
	Type Type
	// Scope は、効く範囲である。
	Scope Scope
	// Default は、既定値である。型は Type と同じにする。
	Default Value
	// Range は、整数の項目が取れる値の範囲である。nil なら範囲を限らない。整数でない項目では nil にする。
	// 範囲は Check で確かめるので、値を項目に入れるどの経路（設定ファイル、:option.set、:set）にも効く。
	Range *IntRange
	// Contains は、文字列の項目の値が空でなければ含まなければならない文字列である。空なら制約を置かない。文字列でない項目では空にする。
	// 値は Check で確かめるので、値を項目に入れるどの経路（設定ファイル、:option.set、:set）にも効く。
	// comment.string の %s に使う（Vim 9.1 と Neovim 0.12.5 の 'commentstring' の E537 と同じ制約）。
	Contains string
	// Detected は、範囲がバッファごとの項目のうち、初めの値をバッファを開くときにファイルから判定して決める項目であることを表す（filetype）。
	// この項目は、設定ファイル（最上位と fileTypeOptions）にも .editorconfig にも書けず、初めの値の段（パッケージの doc）を当てない。
	// 範囲がエディタ全体の項目では false にする。
	Detected bool
}

// IntRange は、整数の項目が取れる値の範囲 [Min, Max] である（両端を含む）。
type IntRange struct {
	Min, Max int
}

// 項目の正規の名前。値を読む側は、名前を文字列で書かずにこの定数を使う。
const (
	// FileType は、バッファのファイルの種類である（基本設計 6.7 のファイルの種類の判定。FR-134）。
	// バッファを開くときに、設定の fileTypes の対応表と自動の判定で初めの値が決まる。ステータス行の部品の名前と同じにした。
	FileType = "filetype"
	// LineNumberShow は、バッファの表示領域の左に行番号の列を出すかである（FR-135。Vim の 'number'）。
	LineNumberShow = "lineNumber.show"
	// LineNumberMinWidth は、行番号の列の最小の列数である（FR-136。Vim の 'numberwidth'）。
	// 列の幅は、この値と、バッファの最後の行の番号の桁数に 1 を足した数の大きい方になる。範囲は Vim と同じく 1 から 20 である。
	LineNumberMinWidth = "lineNumber.minWidth"
	// LineWrapEnabled は、長い行を本文の幅で折り返して描くかである（FR-156。Vim の 'wrap'）。
	// 本体の既定は折り返さない（Vim と Neovim の既定は折り返す。#300 のオーナーの決め）。描画が、描くたびにこの値を読む。
	LineWrapEnabled = "lineWrap.enabled"
	// SearchHighlight は、直前の検索のパターンに一致する範囲に色を付けるかである（FR-137。Vim の 'hlsearch'）。
	// 描画が、描くたびにこの値と直前の検索のパターンを読む。
	SearchHighlight = "search.highlight"
	// SearchIncrementalTimeoutMs は、/ と ? でパターンを打っている間の、打つたびの検索の時間の上限のミリ秒である（FR-153）。
	// 上限を超えたら、その回は一致を出さない。<CR> で決めたときの検索には上限を付けない。範囲は 1 から 10000 である。
	// 既定の 500 は、Vim 9.1 の 'incsearch' が検索を約 0.5 秒で打ち切るのと同じである（:help 'incsearch'。'redrawtime' の既定の 2000 は
	// 'hlsearch' の色付けの上限で、打つたびの検索には使わない）。打つたびの検索をしない設定は持たない（#296 のオーナーの決め）。
	SearchIncrementalTimeoutMs = "search.incrementalTimeoutMs"
	// SearchShowCount は、検索で動いたときに、メッセージの行の右に一致の件数（[2/5] の形）を出すかである（FR-167。
	// Vim の 'shortmess' に S が無いとき）。本体の既定は Vim 9.1 の既定（'shortmess' に S があり、出さない）に合わせて出さない。
	// 検索するたびにこの値を読む。'shortmess' は多くのフラグをまとめた設定なので、:set の別名は作らない（#320 のオーナーの決め）。
	SearchShowCount = "search.showCount"
	// YankHighlightMs は、本体の y でヤンクした範囲に色を付けておくミリ秒である（FR-140。nvim の vim.hl.on_yank の timeout）。
	// 0 なら付けない。範囲は 0 から 10000 である。Vim にこの設定は無い。
	YankHighlightMs = "yank.highlightMs"
	// PreviewMinWindowWidth は、項目のプレビュー（FR-149）を出す窓の幅の境目の列数である。プレビューの欄を持つ中央のポップアップか
	// ペインの窓の幅（PreviewWindowWidthPercent で広げた幅）がこの列数を超えるときだけ、プレビューを右に出す。
	// 既定の 100 は、fzf-lua の flip_columns の既定と同じである。範囲は 0 から 10000 である。Vim にこの設定は無い。
	PreviewMinWindowWidth = "preview.minWindowWidth"
	// PreviewWidthPercent は、プレビューを出すときに、窓の幅のうちプレビューに使う百分率である。
	// 既定の 60 は、fzf-lua の winopts.preview.horizontal の既定（right:60%）と同じである。範囲は 1 から 99 である。
	PreviewWidthPercent = "preview.widthPercent"
	// PreviewWindowWidthPercent は、プレビューを出すときの中央のポップアップの窓の幅の、画面の幅に対する百分率である。
	// 既定の 90 は、オーナーの nvim の fzf-lua の winopts.width と同じである。範囲は 10 から 100 である。
	PreviewWindowWidthPercent = "preview.windowWidthPercent"
	// IconsShow は、一覧と木の、ファイルかディレクトリを指す項目の前に、ファイルの種類のアイコンを付けるかである（FR-152）。
	// 既定は付ける（#295 のオーナーの決め）。アイコンは Nerd Font のグリフなので、Nerd Font の入っていない端末では false にする。
	// 一覧と木を作り直すとき（開いたときと、拡張機能が中身を送り直したとき）に読む。Vim にこの設定は無い。
	IconsShow = "icons.show"
	// IndentUseSpaces は、字下げをスペースで作るかである（FR-160。Vim の 'expandtab'）。範囲はバッファごとで、既定は Vim と同じくタブ文字で作る。
	// true なら、インサートモードの <Tab> と <BS> は Neovim 0.12.5 の expandtab shiftwidth=N softtabstop=N（N は IndentWidth の幅）と同じに、
	// false なら noexpandtab shiftwidth=N softtabstop=0 と同じに動く。どちらも Neovim の既定の 'smarttab' がオンの動きである。
	// Vim の 'softtabstop' と 'smarttab' は、別の設定にしない（#318 の決め）。
	IndentUseSpaces = "indent.useSpaces"
	// IndentWidth は、字下げの 1 段の幅である（FR-160。Vim の 'shiftwidth'）。範囲はバッファごとで、既定は Vim と同じく 8 である。
	// 0 なら TabWidth の値を使う（Vim の shiftwidth=0 と同じ）。範囲は 0 から 9999 である（上限は TabWidth と同じにした）。
	IndentWidth = "indent.width"
	// TabWidth は、タブ文字の表示幅である（FR-161。Vim の 'tabstop'）。範囲はバッファごとで、既定は Vim と同じく 8 である。
	// 描画、カーソルの列の数え方、gj と gk、字下げの幅の数え方が、使うたびにバッファの今の値を読む。範囲は Vim と同じく 1 から 9999 である。
	TabWidth = "tab.width"
	// IndentAuto は、自動インデントを行うかである（FR-162。Vim の 'autoindent'）。範囲はバッファごとで、既定は Vim と同じく行わない。
	IndentAuto = "indent.auto"
	// EditorConfigEnabled は、開いたファイルに当たる .editorconfig を読むかである（FR-164。Neovim の g:editorconfig）。
	// 範囲はエディタ全体で、既定は Neovim と同じく読む。バッファを開くときに読むので、変えた後に開いたバッファから効く。
	EditorConfigEnabled = "editorConfig.enabled"
	// UndoPersist は、ファイルのアンドゥの履歴を、そのファイルを保存するときに状態の置き場（$XDG_STATE_HOME/vi-imitation/undo）へ残し、
	// 次にそのファイルを開いたときに読み戻すかである（FR-165。Vim の 'undofile'）。
	// 既定は Vim と同じく残さない（#319 のオーナーの決め）。false の間は、読みも書きもせず、置き場に何も作らない。
	// 保存するときと開くときに読む。
	UndoPersist = "undo.persist"
	// CursorRestore は、ファイルを閉じたときのカーソルの位置を状態の置き場（$XDG_STATE_HOME/vi-imitation/state.json）へ残し、
	// 次にそのファイルを開いたときにその位置へカーソルを置くかである（FR-166。Vim の defaults.vim の BufReadPost の自動コマンドに当たる）。
	// 既定は Vim の既定（defaults.vim を読まない）と同じく残さない（#319 のオーナーの決め）。false の間は、読みも書きもしない。
	// ファイルを閉じるときと開くときに読む。
	CursorRestore = "cursor.restore"
	// CommentString は、行のコメントの形である（FR-169。Vim の 'commentstring'）。範囲はバッファごとで、%s の前がコメントの前に、
	// 後ろがコメントの後ろに付く（"// %s"、"/* %s */"）。値は空か、%s を含む（Def.Contains。Vim の E537）。
	// 既定は Neovim 0.12.5 と同じく空で、ファイルの種類ごとの値は fileTypeDefaults にある。
	// Vim 9.1 の既定は "/* %s */" だが、Vim 9.1 に既定の gc は無く、本体の gc は Neovim の gc に合わせるので、既定も Neovim に合わせた
	// （空なら gc はコメントを付けずに知らせる。種類の分からないファイルに C のコメントを付けない）。
	// gc とコメントのテキストオブジェクトが、使うたびにバッファの今の値を読む（internal/command の State.commentString）。
	CommentString = "comment.string"
)

// defs は、設定の表である。項目を足すときは、この表に行を足す。
// 順は、Defs が返す一覧の順である。
var defs = []Def{
	{Name: FileType, Type: String, Scope: ScopeBuffer, Default: StringValue(""), Detected: true},
	{Name: LineNumberShow, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	{Name: LineNumberMinWidth, Type: Int, Scope: ScopeEditor, Default: IntValue(4), Range: &IntRange{Min: 1, Max: 20}},
	{Name: LineWrapEnabled, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	{Name: SearchHighlight, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	// 下限は、打つたびの検索をしない値（0）を作らないため 1 にした。上限は yank.highlightMs と同じ 10 秒にした。
	{Name: SearchIncrementalTimeoutMs, Type: Int, Scope: ScopeEditor, Default: IntValue(500), Range: &IntRange{Min: 1, Max: 10000}},
	{Name: SearchShowCount, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	// 上限は、色を一瞬だけ付ける設定なので 10 秒にした（ミリ秒を時間の長さにしたときに溢れないためでもある）。
	{Name: YankHighlightMs, Type: Int, Scope: ScopeEditor, Default: IntValue(0), Range: &IntRange{Min: 0, Max: 10000}},
	// 境目の上限は、端末の列数として現実に無い大きさにした。0 にすると、プレビューの欄を持つ窓では常に出す。
	{Name: PreviewMinWindowWidth, Type: Int, Scope: ScopeEditor, Default: IntValue(100), Range: &IntRange{Min: 0, Max: 10000}},
	{Name: PreviewWidthPercent, Type: Int, Scope: ScopeEditor, Default: IntValue(60), Range: &IntRange{Min: 1, Max: 99}},
	// 下限は、窓が一覧とプレビューに分けられないほど狭くならないように 10 にした。
	{Name: PreviewWindowWidthPercent, Type: Int, Scope: ScopeEditor, Default: IntValue(90), Range: &IntRange{Min: 10, Max: 100}},
	{Name: IconsShow, Type: Bool, Scope: ScopeEditor, Default: BoolValue(true)},
	{Name: IndentUseSpaces, Type: Bool, Scope: ScopeBuffer, Default: BoolValue(false)},
	{Name: IndentWidth, Type: Int, Scope: ScopeBuffer, Default: IntValue(8), Range: &IntRange{Min: 0, Max: 9999}},
	{Name: TabWidth, Type: Int, Scope: ScopeBuffer, Default: IntValue(8), Range: &IntRange{Min: 1, Max: 9999}},
	{Name: IndentAuto, Type: Bool, Scope: ScopeBuffer, Default: BoolValue(false)},
	{Name: EditorConfigEnabled, Type: Bool, Scope: ScopeEditor, Default: BoolValue(true)},
	{Name: UndoPersist, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	{Name: CursorRestore, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	{Name: CommentString, Type: String, Scope: ScopeBuffer, Default: StringValue(""), Contains: "%s"},
}

// fileTypeDefaults は、本体のファイルの種類ごとの既定（パッケージの doc の 3 の段）である。
//
// 字下げ: Vim 9.1 と Neovim 0.12.5 の runtime の ftplugin/make.vim と ftplugin/go.vim は、どちらも
// setlocal noexpandtab softtabstop=0 shiftwidth=0 とする（Makefile のレシピはタブ文字で始める。Go は gofmt がタブ文字で字下げする）。
// 2026-11-21 に読んで写した。タブ文字の表示幅（tab.width）は ftplugin が変えないので、ここでも変えない。
//
// コメントの形（comment.string。FR-169）: 本体のファイルの種類の判定（internal/editor の defaultFileTypes）が決める種類と lua について、
// Vim 9.1 と Neovim 0.12.5 の runtime の ftplugin/<種類>.vim（Neovim は <種類>.lua も）が setlocal commentstring で決める値を写した
// （2026-11-21。setf <種類> の後の &commentstring を両方で読んで確かめた）。
// lua は本体が判定しないが、設定の fileTypes で足すことの多い種類（ui のアイコンの表と同じ）で、#327 の受入条件にあるので入れた。
// Vim と Neovim で値が違うのは c だけで、Vim 9.1 の ftplugin/c.vim は "/* %s */"、Neovim 0.12.5 は ftplugin/c.vim の後に読む
// ftplugin/c.lua が "// %s" にする。本体の gc は Neovim に合わせるので、Neovim の値を写した。
// json の ftplugin（Vim 9.1 と Neovim 0.12.5 の ftplugin/json.vim）は setlocal commentstring= で空にするので、空を入れた
// （設定ファイルの最上位に書いた値も、json では空になる。Neovim の ftplugin と同じ）。
var fileTypeDefaults = map[string]map[string]Value{
	"make":       {IndentUseSpaces: BoolValue(false), IndentWidth: IntValue(0), CommentString: StringValue("# %s")},
	"go":         {IndentUseSpaces: BoolValue(false), IndentWidth: IntValue(0), CommentString: StringValue("// %s")},
	"gomod":      {CommentString: StringValue("// %s")},
	"markdown":   {CommentString: StringValue("<!-- %s -->")},
	"json":       {CommentString: StringValue("")},
	"jsonc":      {CommentString: StringValue("// %s")},
	"python":     {CommentString: StringValue("# %s")},
	"c":          {CommentString: StringValue("// %s")},
	"javascript": {CommentString: StringValue("// %s")},
	"typescript": {CommentString: StringValue("// %s")},
	"rust":       {CommentString: StringValue("// %s")},
	"sh":         {CommentString: StringValue("# %s")},
	"yaml":       {CommentString: StringValue("# %s")},
	"toml":       {CommentString: StringValue("# %s")},
	"lua":        {CommentString: StringValue("-- %s")},
}

// FileTypeDefaults は、本体のファイルの種類 fileType の既定の値（正規の名前から値への対応）の写しを返す。無ければ nil を返す。
// バッファを開くとき（と、ファイルの種類が変わったとき）に、設定ファイルの最上位の値の後に当てる（パッケージの doc の 3 の段）。
func FileTypeDefaults(fileType string) map[string]Value {
	return maps.Clone(fileTypeDefaults[fileType])
}

// Defs は、設定の表の写しを表の順に返す（設定ファイルの読み込みと、後に作るコマンドラインの補完の一覧）。
func Defs() []Def {
	out := make([]Def, len(defs))
	copy(out, defs)
	return out
}

// Lookup は、正規の名前が name の項目を返す。無ければ ok は false である。名前の省略は受け付けない。
func Lookup(name string) (Def, bool) {
	for _, d := range defs {
		if d.Name == name {
			return d, true
		}
	}
	return Def{}, false
}

// 値を読むときの誤り。プログラムの内部の誤りなので文言は英語にし、画面に出す呼び出し側が errors.Is で見分けて、
// 日本語の文言と Vim のエラー番号を付ける。
var (
	// ErrInvalidBool は、真偽の項目の値が true でも false でもないことを表す（Vim の E474 に当たる）。
	ErrInvalidBool = errors.New("option: value must be true or false")
	// ErrNumberRequired は、整数の項目の値が 10 進の整数でないことを表す（Vim の E521 に当たる）。
	ErrNumberRequired = errors.New("option: value must be a decimal integer")
	// ErrTypeMismatch は、Values.Set に渡した値の型が項目の型と違うことを表す（呼び出し側の誤り）。
	ErrTypeMismatch = errors.New("option: value type does not match the option type")
	// ErrOutOfRange は、整数の項目の値が範囲（Def.Range）の外にあることを表す。ErrBelowMin と ErrAboveMax はこれを包む。
	ErrOutOfRange = errors.New("option: value is out of range")
	// ErrBelowMin は、整数の項目の値が範囲の下限より小さいことを表す（下限が 1 の項目では、Vim の E487 に当たる）。
	ErrBelowMin = fmt.Errorf("%w: below the minimum", ErrOutOfRange)
	// ErrAboveMax は、整数の項目の値が範囲の上限より大きいことを表す（Vim の 'numberwidth' の 21 以上と同じく E474 に当たる）。
	ErrAboveMax = fmt.Errorf("%w: above the maximum", ErrOutOfRange)
	// ErrMissingContains は、文字列の項目の空でない値が、含まなければならない文字列（Def.Contains）を含まないことを表す
	// （comment.string では、Vim 9.1 と Neovim 0.12.5 の 'commentstring' の E537 に当たる）。
	ErrMissingContains = errors.New("option: value must be empty or contain the required text")
)

// Check は、値 v が項目 d に入れられる値かを確かめる。型が項目の型と違えば ErrTypeMismatch を返す。
// 整数の項目に範囲（d.Range）があれば、下限より小さい値に ErrBelowMin を、上限より大きい値に ErrAboveMax を返す（FR-136）。
// 文字列の項目に含まなければならない文字列（d.Contains）があれば、それを含まない空でない値に ErrMissingContains を返す（FR-169）。
// 値を項目に入れる経路（コマンドラインの文字列から読む Parse、設定ファイルの JSON の値から読む internal/config、Values.Set）は、
// どれも型の変換の後にこれを通る（FR-131）。項目ごとの値の制約を表に足すときは、項目の名前で分けず、表の欄にしてここで確かめる。
//
// req: FR-131, FR-136, FR-169
func (d Def) Check(v Value) error {
	if v.Type() != d.Type {
		return ErrTypeMismatch
	}
	if d.Contains != "" && v.Type() == String && v.String() != "" && !strings.Contains(v.String(), d.Contains) {
		return ErrMissingContains
	}
	if r := d.Range; r != nil && v.Type() == Int {
		switch {
		case v.Int() < r.Min:
			return ErrBelowMin
		case v.Int() > r.Max:
			return ErrAboveMax
		}
	}
	return nil
}

// Parse は、文字列 text を項目の型の値に変換し、Check で確かめて返す。
// 真偽は true と false だけ、整数は 10 進（先頭の - を許し、+ と 0x は許さない）、文字列は text をそのまま値にする（空も許す）。
// 変換できなければ ErrInvalidBool か ErrNumberRequired を（int に収まらない整数は ParseInt の ErrBelowMin か ErrAboveMax を）、
// 確かめて誤りなら Check の誤りを返す。
func (d Def) Parse(text string) (Value, error) {
	v, err := d.convert(text)
	if err != nil {
		return Value{}, err
	}
	if err := d.Check(v); err != nil {
		return Value{}, err
	}
	return v, nil
}

// convert は、文字列 text を項目の型の値に変換する。値の検査はしない（Check）。
func (d Def) convert(text string) (Value, error) {
	switch d.Type {
	case Bool:
		switch text {
		case "true":
			return BoolValue(true), nil
		case "false":
			return BoolValue(false), nil
		}
		return Value{}, ErrInvalidBool
	case Int:
		n, err := ParseInt(text)
		if err != nil {
			return Value{}, err
		}
		return IntValue(n), nil
	case String:
		return StringValue(text), nil
	}
	return Value{}, ErrTypeMismatch
}

// ParseInt は、整数の項目の値の文字列 text を読む。10 進（先頭の - を許し、+ と 0x は許さない）で、読めなければ ErrNumberRequired を返す。
// 数字だけの文字列が int に収まらなければ、符号で、正なら ErrAboveMax、負なら ErrBelowMin を返す（どちらも ErrOutOfRange を包む）。
// 収まらない値はどの範囲の外でもあるとして扱うためで、Vim 9.1 も :set numberwidth=99999999999999999999 を E474、
// -99999999999999999999 を E487 にする（2026-11-20 に確かめた。PR #272 のレビューの推奨 2）。
// コマンドラインの文字列（Def.Parse）と設定ファイルの JSON の数（internal/config）は、どちらもこれで読む。項目の名前では分けない。
//
// req: FR-136
func ParseInt(text string) (int, error) {
	if text == "" || text[0] == '+' {
		return 0, ErrNumberRequired
	}
	n, err := strconv.Atoi(text)
	switch {
	case errors.Is(err, strconv.ErrRange) && text[0] == '-':
		return 0, ErrBelowMin
	case errors.Is(err, strconv.ErrRange):
		return 0, ErrAboveMax
	case err != nil:
		return 0, ErrNumberRequired
	}
	return n, nil
}

// Values は、1 つの範囲（エディタ全体か、1 つのバッファ）の項目の今の値である。
// 値を設定していない項目は、表の既定値を持つ。ゼロ値は、すべての項目が既定値の状態である。
// 範囲は呼び出し側が決める（エディタ全体の Values には ScopeEditor の項目だけを、バッファの Values には ScopeBuffer の項目だけを入れる）。
type Values struct {
	m map[string]Value
	// sets は、項目ごとに Set で値を入れた回数である（Revision）。
	sets map[string]uint64
}

// Get は、項目 name の今の値を返す。表に無い名前ならゼロ値を返す。
func (vs *Values) Get(name string) Value {
	if v, ok := vs.m[name]; ok {
		return v
	}
	d, _ := Lookup(name)
	return d.Default
}

// Lookup は、項目 name に Set で値を入れてあれば、その値と true を返す。入れていなければ（表の既定値のままなら）、ゼロ値と false を返す。
// 2 つの Values を重ねて読む使い方（利用者が :set で変えた値を、初めの値の段の値より優先する。internal/editor）に使う。
func (vs *Values) Lookup(name string) (Value, bool) {
	v, ok := vs.m[name]
	return v, ok
}

// Set は、項目 d の値を v にする。v が d に入れられない値（Def.Check。型が違うなど）なら、値を変えずにその誤りを返す。
func (vs *Values) Set(d Def, v Value) error {
	if err := d.Check(v); err != nil {
		return err
	}
	if vs.m == nil {
		vs.m, vs.sets = map[string]Value{}, map[string]uint64{}
	}
	vs.m[d.Name] = v
	vs.sets[d.Name]++
	return nil
}

// Revision は、項目 name に Set で値を入れた回数を返す。前と同じ値を入れても数える。誤りで入れなかった Set は数えない。
// 値が同じままでも「設定し直した」ことを、使う側が見分けるための数である。
// 一致の色を消した状態（internal/command/ex の :search.clearHighlight。FR-138）は、消したときの search.highlight の回数を覚えておき、
// 回数が変われば（:set hlsearch を打ち直せば）消した状態を効かなくする。項目の名前によらず、どの項目も数える。
//
// req: FR-138
func (vs *Values) Revision(name string) uint64 {
	return vs.sets[name]
}

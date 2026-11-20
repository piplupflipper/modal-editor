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
package option

import (
	"errors"
	"fmt"
	"strconv"
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
	// ScopeBuffer は、バッファごとに値を持つ項目である。初めの値はバッファを開くときに決まり、設定ファイルには書けない。
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
	// SearchHighlight は、直前の検索のパターンに一致する範囲に色を付けるかである（FR-137。Vim の 'hlsearch'）。
	// 描画が、描くたびにこの値と直前の検索のパターンを読む。
	SearchHighlight = "search.highlight"
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
)

// defs は、設定の表である。項目を足すときは、この表に行を足す。
// 順は、Defs が返す一覧の順である。
var defs = []Def{
	{Name: FileType, Type: String, Scope: ScopeBuffer, Default: StringValue("")},
	{Name: LineNumberShow, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	{Name: LineNumberMinWidth, Type: Int, Scope: ScopeEditor, Default: IntValue(4), Range: &IntRange{Min: 1, Max: 20}},
	{Name: SearchHighlight, Type: Bool, Scope: ScopeEditor, Default: BoolValue(false)},
	// 上限は、色を一瞬だけ付ける設定なので 10 秒にした（ミリ秒を時間の長さにしたときに溢れないためでもある）。
	{Name: YankHighlightMs, Type: Int, Scope: ScopeEditor, Default: IntValue(0), Range: &IntRange{Min: 0, Max: 10000}},
	// 境目の上限は、端末の列数として現実に無い大きさにした。0 にすると、プレビューの欄を持つ窓では常に出す。
	{Name: PreviewMinWindowWidth, Type: Int, Scope: ScopeEditor, Default: IntValue(100), Range: &IntRange{Min: 0, Max: 10000}},
	{Name: PreviewWidthPercent, Type: Int, Scope: ScopeEditor, Default: IntValue(60), Range: &IntRange{Min: 1, Max: 99}},
	// 下限は、窓が一覧とプレビューに分けられないほど狭くならないように 10 にした。
	{Name: PreviewWindowWidthPercent, Type: Int, Scope: ScopeEditor, Default: IntValue(90), Range: &IntRange{Min: 10, Max: 100}},
	{Name: IconsShow, Type: Bool, Scope: ScopeEditor, Default: BoolValue(true)},
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
)

// Check は、値 v が項目 d に入れられる値かを確かめる。型が項目の型と違えば ErrTypeMismatch を返す。
// 整数の項目に範囲（d.Range）があれば、下限より小さい値に ErrBelowMin を、上限より大きい値に ErrAboveMax を返す（FR-136）。
// 値を項目に入れる経路（コマンドラインの文字列から読む Parse、設定ファイルの JSON の値から読む internal/config、Values.Set）は、
// どれも型の変換の後にこれを通る（FR-131）。項目ごとの値の制約を表に足すときは、項目の名前で分けず、表の欄にしてここで確かめる。
//
// req: FR-131, FR-136
func (d Def) Check(v Value) error {
	if v.Type() != d.Type {
		return ErrTypeMismatch
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

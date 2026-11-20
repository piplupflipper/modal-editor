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
}

// 項目の正規の名前。値を読む側は、名前を文字列で書かずにこの定数を使う。
const (
	// FileType は、バッファのファイルの種類である（基本設計 6.7 のファイルの種類の判定。FR-134）。
	// バッファを開くときに、設定の fileTypes の対応表と自動の判定で初めの値が決まる。ステータス行の部品の名前と同じにした。
	FileType = "filetype"
)

// defs は、設定の表である。項目を足すときは、この表に行を足す。
// 順は、Defs が返す一覧の順である。
var defs = []Def{
	{Name: FileType, Type: String, Scope: ScopeBuffer, Default: StringValue("")},
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
)

// Parse は、文字列 text を項目の型の値として読む。
// 真偽は true と false だけ、整数は 10 進（先頭の - を許し、+ と 0x は許さない）、文字列は text をそのまま値にする（空も許す）。
// 読めなければ ErrInvalidBool か ErrNumberRequired を返す。
func (d Def) Parse(text string) (Value, error) {
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
		if text == "" || text[0] == '+' {
			return Value{}, ErrNumberRequired
		}
		n, err := strconv.Atoi(text)
		if err != nil {
			return Value{}, ErrNumberRequired
		}
		return IntValue(n), nil
	case String:
		return StringValue(text), nil
	}
	return Value{}, ErrTypeMismatch
}

// Values は、1 つの範囲（エディタ全体か、1 つのバッファ）の項目の今の値である。
// 値を設定していない項目は、表の既定値を持つ。ゼロ値は、すべての項目が既定値の状態である。
// 範囲は呼び出し側が決める（エディタ全体の Values には ScopeEditor の項目だけを、バッファの Values には ScopeBuffer の項目だけを入れる）。
type Values struct {
	m map[string]Value
}

// Get は、項目 name の今の値を返す。表に無い名前ならゼロ値を返す。
func (vs *Values) Get(name string) Value {
	if v, ok := vs.m[name]; ok {
		return v
	}
	d, _ := Lookup(name)
	return d.Default
}

// Set は、項目 d の値を v にする。v の型が d の型と違えば、値を変えずに ErrTypeMismatch を返す。
func (vs *Values) Set(d Def, v Value) error {
	if v.Type() != d.Type {
		return ErrTypeMismatch
	}
	if vs.m == nil {
		vs.m = map[string]Value{}
	}
	vs.m[d.Name] = v
	return nil
}

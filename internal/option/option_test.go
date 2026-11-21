package option

import (
	"errors"
	"regexp"
	"strconv"
	"testing"
)

// 設定の表の各行は、名前、型、範囲、既定値を持ち、既定値の型は項目の型と同じである。
// 名前は点で区切った camelCase で、重ならない。
//
// req: FR-131
func TestDefs(t *testing.T) {
	name := regexp.MustCompile(`^[a-z][a-zA-Z0-9]*(\.[a-z][a-zA-Z0-9]*)*$`)
	seen := map[string]bool{}
	for _, d := range Defs() {
		if !name.MatchString(d.Name) {
			t.Errorf("%q: 名前が点で区切った camelCase でない", d.Name)
		}
		if seen[d.Name] {
			t.Errorf("%q: 名前が重なる", d.Name)
		}
		seen[d.Name] = true
		if d.Type < Bool || d.Type > String {
			t.Errorf("%s: 型 = %v", d.Name, d.Type)
		}
		if d.Scope != ScopeEditor && d.Scope != ScopeBuffer {
			t.Errorf("%s: 範囲 = %v", d.Name, d.Scope)
		}
		if d.Default.Type() != d.Type {
			t.Errorf("%s: 既定値の型 = %v, want %v", d.Name, d.Default.Type(), d.Type)
		}
		if d.Range != nil && (d.Type != Int || d.Range.Min > d.Range.Max) {
			t.Errorf("%s: 範囲 = %+v（型 %v）", d.Name, *d.Range, d.Type)
		}
		if err := d.Check(d.Default); err != nil {
			t.Errorf("%s: 既定値 %v が検査を通らない: %v", d.Name, d.Default, err)
		}
		if got, ok := Lookup(d.Name); !ok || got != d {
			t.Errorf("Lookup(%q) = %+v, %v", d.Name, got, ok)
		}
	}
	if d, ok := Lookup(FileType); !ok || d.Type != String || d.Scope != ScopeBuffer || d.Default.String() != "" {
		t.Errorf("Lookup(FileType) = %+v, %v", d, ok)
	}
	if _, ok := Lookup("file"); ok {
		t.Error("名前の省略を受け付けた")
	}
}

// 行番号の 2 つの項目は、範囲がエディタ全体で、既定は出さない、最小の列数は 4（1 から 20）である。
//
// req: FR-135, FR-136
func TestLineNumberDefs(t *testing.T) {
	if d, ok := Lookup(LineNumberShow); !ok || d.Type != Bool || d.Scope != ScopeEditor || d.Default != BoolValue(false) || d.Range != nil {
		t.Errorf("Lookup(LineNumberShow) = %+v, %v", d, ok)
	}
	d, ok := Lookup(LineNumberMinWidth)
	if !ok || d.Type != Int || d.Scope != ScopeEditor || d.Default != IntValue(4) || d.Range == nil || *d.Range != (IntRange{Min: 1, Max: 20}) {
		t.Errorf("Lookup(LineNumberMinWidth) = %+v, %v", d, ok)
	}
}

// 検索の一致の色付けの項目は、範囲がエディタ全体の真偽で、既定は付けない（Vim の 'hlsearch' と同じ）。
//
// req: FR-137
func TestSearchHighlightDef(t *testing.T) {
	if d, ok := Lookup(SearchHighlight); !ok || d.Type != Bool || d.Scope != ScopeEditor || d.Default != BoolValue(false) || d.Range != nil {
		t.Errorf("Lookup(SearchHighlight) = %+v, %v", d, ok)
	}
}

// ヤンクした範囲の色付けの項目は、範囲がエディタ全体の整数で、既定は 0（付けない）、範囲は 0 から 10000 である。
// 範囲の外の値は、項目の名前によらない範囲の検査（Check）で断る。
//
// req: FR-140
func TestYankHighlightMsDef(t *testing.T) {
	d, ok := Lookup(YankHighlightMs)
	if !ok || d.Type != Int || d.Scope != ScopeEditor || d.Default != IntValue(0) || d.Range == nil || *d.Range != (IntRange{Min: 0, Max: 10000}) {
		t.Fatalf("Lookup(YankHighlightMs) = %+v, %v", d, ok)
	}
	for _, tt := range []struct {
		text string
		want error
	}{
		{"0", nil}, {"300", nil}, {"10000", nil}, {"-1", ErrBelowMin}, {"10001", ErrAboveMax},
	} {
		if _, err := d.Parse(tt.text); !errors.Is(err, tt.want) {
			t.Errorf("Parse(%q) = %v, want %v", tt.text, err, tt.want)
		}
	}
}

// プレビューの幅の項目は、範囲がエディタ全体の整数で、既定は fzf-lua の既定とオーナーの nvim の窓の幅（100、60、90）である。
func TestPreviewDefs(t *testing.T) {
	for _, tt := range []struct {
		name     string
		def      int
		min, max int
	}{
		{PreviewMinWindowWidth, 100, 0, 10000},
		{PreviewWidthPercent, 60, 1, 99},
		{PreviewWindowWidthPercent, 90, 10, 100},
	} {
		d, ok := Lookup(tt.name)
		if !ok || d.Type != Int || d.Scope != ScopeEditor || d.Default != IntValue(tt.def) || d.Range == nil || *d.Range != (IntRange{Min: tt.min, Max: tt.max}) {
			t.Errorf("Lookup(%s) = %+v, %v", tt.name, d, ok)
			continue
		}
		if _, err := d.Parse(strconv.Itoa(tt.min - 1)); !errors.Is(err, ErrBelowMin) {
			t.Errorf("%s: Parse(%d) = %v", tt.name, tt.min-1, err)
		}
		if _, err := d.Parse(strconv.Itoa(tt.max + 1)); !errors.Is(err, ErrAboveMax) {
			t.Errorf("%s: Parse(%d) = %v", tt.name, tt.max+1, err)
		}
	}
}

// カーソルが止まった契機の時間と診断のポップアップの項目は、範囲がエディタ全体で、既定は Vim と Neovim と同じく
// 4000 ミリ秒と出さないである。時間の範囲は 0 から 60000 で、外の値は範囲の検査で断る。
func TestCursorHoldDefs(t *testing.T) {
	d, ok := Lookup(CursorHoldDelayMs)
	if !ok || d.Type != Int || d.Scope != ScopeEditor || d.Default != IntValue(4000) || d.Range == nil || *d.Range != (IntRange{Min: 0, Max: 60000}) {
		t.Fatalf("Lookup(CursorHoldDelayMs) = %+v, %v", d, ok)
	}
	for _, tt := range []struct {
		text string
		want error
	}{
		{"0", nil}, {"100", nil}, {"60000", nil}, {"-1", ErrBelowMin}, {"60001", ErrAboveMax},
	} {
		if _, err := d.Parse(tt.text); !errors.Is(err, tt.want) {
			t.Errorf("Parse(%q) = %v, want %v", tt.text, err, tt.want)
		}
	}
	if d, ok := Lookup(DiagnosticPopup); !ok || d.Type != Bool || d.Scope != ScopeEditor || d.Default != BoolValue(false) || d.Range != nil {
		t.Errorf("Lookup(DiagnosticPopup) = %+v, %v", d, ok)
	}
}

// Revision は、項目ごとに Set で値を入れた回数で、同じ値を入れ直しても増え、誤りで入れなかった Set と他の項目の Set では増えない。
//
// req: FR-138
func TestValuesRevision(t *testing.T) {
	b := Def{Name: "b", Type: Bool, Default: BoolValue(false)}
	n := Def{Name: "n", Type: Int, Default: IntValue(0)}
	var vs Values
	if got := vs.Revision("b"); got != 0 {
		t.Errorf("ゼロ値の Revision = %d", got)
	}
	steps := []struct {
		d    Def
		v    Value
		want uint64 // b の回数
	}{
		{b, BoolValue(true), 1},
		{b, BoolValue(true), 2},
		{n, IntValue(3), 2},
		{b, IntValue(1), 2}, // 型の違う値は入れない
		{b, BoolValue(false), 3},
	}
	for i, s := range steps {
		_ = vs.Set(s.d, s.v)
		if got := vs.Revision("b"); got != s.want {
			t.Errorf("%d: Revision(b) = %d, want %d", i, got, s.want)
		}
	}
	if got := vs.Revision("n"); got != 1 {
		t.Errorf("Revision(n) = %d, want 1", got)
	}
}

// Check は、整数の項目の範囲（Range）の外の値を、下限の側と上限の側で分けて断る。範囲の両端は通す。
// Parse と Values.Set も同じ検査を通る。
//
// req: FR-136
func TestCheckRange(t *testing.T) {
	d := Def{Name: "n", Type: Int, Default: IntValue(4), Range: &IntRange{Min: 1, Max: 20}}
	tests := []struct {
		n    int
		want error
	}{
		{-1, ErrBelowMin},
		{0, ErrBelowMin},
		{1, nil},
		{20, nil},
		{21, ErrAboveMax},
	}
	for _, tt := range tests {
		err := d.Check(IntValue(tt.n))
		if !errors.Is(err, tt.want) || (tt.want != nil && !errors.Is(err, ErrOutOfRange)) {
			t.Errorf("Check(%d) = %v, want %v", tt.n, err, tt.want)
		}
		if _, err := d.Parse(strconv.Itoa(tt.n)); !errors.Is(err, tt.want) {
			t.Errorf("Parse(%d) = %v, want %v", tt.n, err, tt.want)
		}
		var vs Values
		if err := vs.Set(d, IntValue(tt.n)); !errors.Is(err, tt.want) {
			t.Errorf("Set(%d) = %v, want %v", tt.n, err, tt.want)
		}
	}
	// 範囲の無い整数の項目は、どの値も通す。
	free := Def{Name: "m", Type: Int, Default: IntValue(0)}
	if err := free.Check(IntValue(-1000)); err != nil {
		t.Errorf("範囲の無い項目の Check(-1000) = %v", err)
	}
}

// Parse は、値を項目の型で読む。String は Parse で読み戻すと同じ値になる形を返す。
//
// req: FR-131
func TestParse(t *testing.T) {
	b := Def{Name: "b", Type: Bool, Default: BoolValue(false)}
	n := Def{Name: "n", Type: Int, Default: IntValue(0)}
	s := Def{Name: "s", Type: String, Default: StringValue("")}
	tests := []struct {
		def     Def
		text    string
		want    Value
		wantErr error
	}{
		{b, "true", BoolValue(true), nil},
		{b, "false", BoolValue(false), nil},
		{b, "True", Value{}, ErrInvalidBool},
		{b, "1", Value{}, ErrInvalidBool},
		{b, "", Value{}, ErrInvalidBool},
		{n, "42", IntValue(42), nil},
		{n, "-7", IntValue(-7), nil},
		{n, "007", IntValue(7), nil},
		{n, "+7", Value{}, ErrNumberRequired},
		{n, "0x10", Value{}, ErrNumberRequired},
		{n, "4x", Value{}, ErrNumberRequired},
		{n, "", Value{}, ErrNumberRequired},
		// int に収まらない数字の列は、符号で範囲の外の誤りにする（Vim 9.1 の E474 と E487 に合わせる。範囲の無い項目でも同じ）。
		{n, "99999999999999999999", Value{}, ErrAboveMax},
		{n, "-99999999999999999999", Value{}, ErrBelowMin},
		{n, "9223372036854775807", IntValue(9223372036854775807), nil},
		{n, "1e1", Value{}, ErrNumberRequired},
		{n, "1.5", Value{}, ErrNumberRequired},
		{s, "go", StringValue("go"), nil},
		{s, "", StringValue(""), nil},
		{s, " a|b ", StringValue(" a|b "), nil},
	}
	for _, tt := range tests {
		got, err := tt.def.Parse(tt.text)
		if !errors.Is(err, tt.wantErr) || got != tt.want {
			t.Errorf("%v.Parse(%q) = %+v, %v; want %+v, %v", tt.def.Type, tt.text, got, err, tt.want, tt.wantErr)
		}
		if err == nil {
			if back, err := tt.def.Parse(got.String()); err != nil || back != got {
				t.Errorf("%v.Parse(%q) の String を読み戻すと %+v, %v", tt.def.Type, tt.text, back, err)
			}
		}
	}
}

// Check は、型が項目の型と違う値を断る。Parse と Values.Set は Check を通る。
//
// req: FR-131
func TestCheck(t *testing.T) {
	d, _ := Lookup(FileType)
	if err := d.Check(StringValue("go")); err != nil {
		t.Errorf("Check(文字列) = %v", err)
	}
	for _, v := range []Value{BoolValue(true), IntValue(1), {}} {
		if err := d.Check(v); !errors.Is(err, ErrTypeMismatch) {
			t.Errorf("Check(%+v) = %v, want ErrTypeMismatch", v, err)
		}
	}
}

// Values は、設定していない項目に表の既定値を返し、型の違う値を設定しない。
//
// req: FR-131
func TestValues(t *testing.T) {
	var vs Values
	d, _ := Lookup(FileType)
	if got := vs.Get(FileType); got != d.Default {
		t.Errorf("ゼロ値の Get = %+v", got)
	}
	if err := vs.Set(d, StringValue("go")); err != nil {
		t.Fatal(err)
	}
	if got := vs.Get(FileType).String(); got != "go" {
		t.Errorf("Get = %q", got)
	}
	if err := vs.Set(d, BoolValue(true)); !errors.Is(err, ErrTypeMismatch) {
		t.Errorf("型の違う値の Set: err = %v", err)
	}
	if got := vs.Get(FileType).String(); got != "go" {
		t.Errorf("型の違う値の Set の後の Get = %q", got)
	}
	if got := vs.Get("nosuch"); got.Valid() {
		t.Errorf("表に無い名前の Get = %+v", got)
	}
}

// comment.string は、範囲がバッファごとの文字列で、既定は空（Neovim 0.12.5 と同じ）である。
// 空でない値は %s を含まなければならず（Vim 9.1 と Neovim 0.12.5 の E537）、含まなければ Check、Parse、Values.Set が断る。
//
// req: FR-169
func TestCommentStringDef(t *testing.T) {
	d, ok := Lookup(CommentString)
	if !ok || d.Type != String || d.Scope != ScopeBuffer || d.Default != StringValue("") || d.Contains != "%s" || d.Detected {
		t.Fatalf("Lookup(CommentString) = %+v, %v", d, ok)
	}
	for _, tt := range []struct {
		text string
		want error
	}{
		{"", nil},
		{"// %s", nil},
		{"%s", nil},
		{"/*%s*/", nil},
		{"# %s %s", nil},
		{"//", ErrMissingContains},
		{"% s", ErrMissingContains},
		{" ", ErrMissingContains},
	} {
		if _, err := d.Parse(tt.text); !errors.Is(err, tt.want) {
			t.Errorf("Parse(%q) = %v, want %v", tt.text, err, tt.want)
		}
		var vs Values
		if err := vs.Set(d, StringValue(tt.text)); !errors.Is(err, tt.want) {
			t.Errorf("Set(%q) = %v, want %v", tt.text, err, tt.want)
		}
	}
}

// 本体のファイルの種類ごとの既定は、範囲がバッファごとの項目の、検査を通る値だけを持つ。コメントの形は Neovim 0.12.5 の ftplugin の値である。
// FileTypeDefaults は写しを返すので、呼び出し側が変えても表は変わらない。
//
// req: FR-163, FR-169
func TestFileTypeDefaults(t *testing.T) {
	for ft, vals := range fileTypeDefaults {
		for name, v := range vals {
			d, ok := Lookup(name)
			if !ok || d.Scope != ScopeBuffer || d.Detected {
				t.Errorf("%s: %s は範囲がバッファごとの項目でない", ft, name)
				continue
			}
			if err := d.Check(v); err != nil {
				t.Errorf("%s: %s = %q: %v", ft, name, v, err)
			}
		}
	}
	for ft, want := range map[string]string{
		"go": "// %s", "python": "# %s", "lua": "-- %s", "sh": "# %s", "c": "// %s", "markdown": "<!-- %s -->", "json": "",
	} {
		got, ok := FileTypeDefaults(ft)[CommentString]
		if !ok || got != StringValue(want) {
			t.Errorf("FileTypeDefaults(%q)[comment.string] = %q, %v; want %q", ft, got, ok, want)
		}
	}
	if _, ok := FileTypeDefaults("text")[CommentString]; ok {
		t.Error("text に comment.string の既定がある")
	}
	m := FileTypeDefaults("go")
	m[CommentString] = StringValue("x %s")
	if got := FileTypeDefaults("go")[CommentString]; got != StringValue("// %s") {
		t.Errorf("写しを変えると表が変わった: %q", got)
	}
}

// 拡張機能 API の JSON の値は、項目の型の JSON の値だけを受け付け、:option.set と同じ Check を通す（#349）。
// 書き出した JSON は、読み戻すと同じ値になる。
//
// req: FR-190
func TestParseJSON(t *testing.T) {
	width, _ := Lookup(IndentWidth)
	spaces, _ := Lookup(IndentUseSpaces)
	cms, _ := Lookup(CommentString)
	for _, c := range []struct {
		d    Def
		raw  string
		want Value
		err  error
	}{
		{spaces, "true", BoolValue(true), nil},
		{spaces, " false ", BoolValue(false), nil},
		{spaces, `"true"`, Value{}, ErrInvalidBool},
		{spaces, "1", Value{}, ErrInvalidBool},
		{width, "2", IntValue(2), nil},
		{width, "2.0", Value{}, ErrNumberRequired},
		{width, "1e3", Value{}, ErrNumberRequired},
		{width, `"2"`, Value{}, ErrNumberRequired},
		{width, "-1", Value{}, ErrBelowMin},
		{width, "10000", Value{}, ErrAboveMax},
		{width, "99999999999999999999", Value{}, ErrAboveMax},
		{cms, `"# %s"`, StringValue("# %s"), nil},
		{cms, `"# %s"`, StringValue("# %s"), nil},
		{cms, `""`, StringValue(""), nil},
		{cms, `"//"`, Value{}, ErrMissingContains},
		{cms, "3", Value{}, ErrStringRequired},
		{cms, `"x`, Value{}, ErrStringRequired},
	} {
		got, err := c.d.ParseJSON([]byte(c.raw))
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s.ParseJSON(%s) = %v, %v; want %v, %v", c.d.Name, c.raw, got, err, c.want, c.err)
		}
		if err != nil {
			continue
		}
		out, err := got.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if back, err := c.d.ParseJSON(out); err != nil || back != got {
			t.Errorf("%s: %s を読み戻すと %v, %v", c.d.Name, out, back, err)
		}
	}
	if out, _ := (Value{}).MarshalJSON(); string(out) != "null" {
		t.Errorf("ゼロ値の JSON = %s, want null", out)
	}
}

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
		{n, "99999999999999999999", Value{}, ErrNumberRequired},
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

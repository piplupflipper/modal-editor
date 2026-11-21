package comment

import (
	"errors"
	"slices"
	"testing"
)

// Parse は、最初の %s の前と後ろに分ける。空なら ErrEmpty と空の形を、%s が無ければ ErrInvalid を返す。
//
// req: FR-169
func TestParse(t *testing.T) {
	for _, tt := range []struct {
		cs      string
		want    Parts
		wantErr error
	}{
		{"// %s", Parts{"// ", ""}, nil},
		{"/* %s */", Parts{"/* ", " */"}, nil},
		{"# %s %s", Parts{"# ", " %s"}, nil},
		{"a%%s", Parts{"a%", ""}, nil},
		{"%s", Parts{}, nil},
		{"", Parts{}, ErrEmpty},
		{"//", Parts{}, ErrInvalid},
	} {
		got, err := Parse(tt.cs)
		if got != tt.want || !errors.Is(err, tt.wantErr) {
			t.Errorf("Parse(%q) = %+v, %v; want %+v, %v", tt.cs, got, err, tt.want, tt.wantErr)
		}
	}
}

// 前と後ろが空白だけの形は、どの行もコメントの行に当たる（Neovim と同じ）。HasMarker は、その形を見分ける。
//
// req: FR-169
func TestHasMarker(t *testing.T) {
	for cs, want := range map[string]bool{"// %s": true, "%s //": true, "%s": false, " %s\t": false} {
		p, _ := Parse(cs)
		if got := p.HasMarker(); got != want {
			t.Errorf("Parse(%q).HasMarker() = %v, want %v", cs, got, want)
		}
		check, err := p.Checker()
		if err != nil {
			t.Fatalf("Parse(%q).Checker(): %v", cs, err)
		}
		if !want && !check("abc") {
			t.Errorf("Parse(%q).Checker() が abc に当たらない", cs)
		}
	}
	var zero Parts
	if check, err := zero.Checker(); zero.HasMarker() || err != nil || !check("x") {
		t.Error("ゼロ値の形")
	}
}

// 前か後ろに不正な UTF-8 のバイトがある形は、Parse が ErrInvalidUTF8 で断る。Parts を直に作ったときも、Checker と Toggle は
// panic せずに ErrInvalidUTF8 を返す（PR #331 のレビューの推奨 2。それまでは regexp.MustCompile が panic した）。
// 行の中の不正な UTF-8 のバイトは、形が正しければそのまま保つ。
//
// req: FR-169
func TestInvalidUTF8(t *testing.T) {
	for _, cs := range []string{"\xff %s", "// %s \xfe", "\xc3%s"} {
		if _, err := Parse(cs); !errors.Is(err, ErrInvalidUTF8) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalidUTF8", cs, err)
		}
	}
	for _, p := range []Parts{{Left: "\xff "}, {Left: "// ", Right: " \xfe"}} {
		if _, err := p.Checker(); !errors.Is(err, ErrInvalidUTF8) {
			t.Errorf("%+v.Checker() err = %v", p, err)
		}
		if got, err := p.Toggle([]string{"a"}); !errors.Is(err, ErrInvalidUTF8) || got != nil {
			t.Errorf("%+v.Toggle() = %q, %v", p, got, err)
		}
	}
	p, _ := Parse("// %s")
	got, err := p.Toggle([]string{"\xffa", "// \xffb"})
	if want := []string{"// \xffa", "// // \xffb"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Toggle = %q, %v; want %q", got, err, want)
	}
}

// Toggle は、引数の行の並びを変えず、行の数を保つ。垂直タブ（Lua の %s に含まれる）も空白として扱う。
//
// req: FR-169
func TestToggleKeepsInput(t *testing.T) {
	p, _ := Parse("// %s")
	in := []string{"  a", "", "\vb"}
	got, err := p.Toggle(in)
	if want := []string{"\v//  a", "\v//", "\v// b"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("Toggle = %q, %v; want %q", got, err, want)
	}
	if !slices.Equal(in, []string{"  a", "", "\vb"}) {
		t.Errorf("引数が変わった: %q", in)
	}
	if back, err := p.Toggle(got); err != nil || !slices.Equal(back, []string{"\v a", "", "\vb"}) {
		t.Errorf("戻した Toggle = %q, %v", back, err)
	}
}

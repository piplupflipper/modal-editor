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
		if !want && !p.Checker()("abc") {
			t.Errorf("Parse(%q).Checker() が abc に当たらない", cs)
		}
	}
	var zero Parts
	if zero.HasMarker() || !zero.Checker()("x") {
		t.Error("ゼロ値の形")
	}
}

// Toggle は、引数の行の並びを変えず、行の数を保つ。垂直タブ（Lua の %s に含まれる）も空白として扱う。
//
// req: FR-169
func TestToggleKeepsInput(t *testing.T) {
	p, _ := Parse("// %s")
	in := []string{"  a", "", "\vb"}
	got := p.Toggle(in)
	if want := []string{"\v//  a", "\v//", "\v// b"}; !slices.Equal(got, want) {
		t.Errorf("Toggle = %q, want %q", got, want)
	}
	if !slices.Equal(in, []string{"  a", "", "\vb"}) {
		t.Errorf("引数が変わった: %q", in)
	}
	if back := p.Toggle(got); !slices.Equal(back, []string{"\v a", "", "\vb"}) {
		t.Errorf("戻した Toggle = %q", back)
	}
}

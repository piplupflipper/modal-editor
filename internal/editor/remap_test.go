package editor

import (
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// 利用者のキーマップの展開。接頭辞を溜め、進めなくなったら確定させる。展開した列は展開し直さない。
//
// req: FR-41
func TestRemapper(t *testing.T) {
	r, errs := newRemapper(map[string]map[string]string{
		"normal": {"ab": "X", "abc": "Y", "Q": "ab", "<Foo>": "x", "<F5>": "dd", "<Insert>": "i"},
		"nomode": {"a": "b"},
	})
	if len(errs) != 2 {
		t.Errorf("errs = %v", errs)
	}
	feed := func(keys string) string {
		var out []editcmd.Key
		for _, k := range editcmd.MustParseKeys(keys) {
			out = append(out, r.feed(mapNormal, k)...)
		}
		return editcmd.KeysString(out)
	}
	tests := []struct{ in, want, flushed string }{
		{"abc", "Y", ""},
		{"ab", "", "X"},
		{"abd", "Xd", ""},
		{"ad", "ad", ""},
		{"Q", "ab", ""},
		{"zz", "zz", ""},
		{"a", "", "a"},
		// F キーと Insert は本体の既定の割り当てが無く、利用者のキーマップで使う（#112）。
		{"<F5>", "dd", ""},
		{"<Insert>", "i", ""},
	}
	for _, tt := range tests {
		if got := feed(tt.in); got != tt.want {
			t.Errorf("%s: feed = %q, want %q", tt.in, got, tt.want)
		}
		if got := editcmd.KeysString(r.flush()); got != tt.flushed {
			t.Errorf("%s: flush = %q, want %q", tt.in, got, tt.flushed)
		}
	}
	// モードの無い場面では展開しない。
	if got := r.feed(mapNone, editcmd.Rune('Q')); editcmd.KeysString(got) != "Q" {
		t.Errorf("mapNone: %v", got)
	}
}

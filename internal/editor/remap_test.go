package editor

import (
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// 利用者のキーマップの左辺と右辺の <Leader> は、leader に置き換える。
// 同じキーマップでも leader を変えれば、<Leader> を書いた割り当てはすべて新しい leader で始まる。
//
// req: FR-97
func TestRemapperLeader(t *testing.T) {
	keymap := map[string]map[string]string{
		"normal": {"<Leader>w": ":w<CR>", "<leader>x": "dd", "Q": "<Leader>w"},
	}
	cases := []struct {
		leader string
		in     []string
		want   []string
	}{
		{"", []string{"<Space>w", "<Space>x", "Q"}, []string{":w<CR>", "dd", "<Space>w"}},
		{",", []string{",w", ",x", "Q", "<Space>w"}, []string{":w<CR>", "dd", ",w", "<Space>w"}},
		{"<C-k>a", []string{"<C-k>aw", "<C-k>ax", "Q"}, []string{":w<CR>", "dd", "<C-k>aw"}},
	}
	for _, c := range cases {
		leader, err := leaderKeys(c.leader)
		if err != nil {
			t.Fatal(err)
		}
		r, errs := newRemapper(keymap, leader)
		if len(errs) != 0 {
			t.Fatalf("leader %q: errs = %v", c.leader, errs)
		}
		for i, in := range c.in {
			var out []editcmd.Key
			for _, k := range editcmd.MustParseKeys(in) {
				out = append(out, r.feed(mapNormal, k)...)
			}
			out = append(out, r.flush()...)
			if got := editcmd.KeysString(out); got != c.want[i] {
				t.Errorf("leader %q: %s → %q, want %q", c.leader, in, got, c.want[i])
			}
		}
	}
}

// 設定の leader が読めなければ、誤りを返して既定の <Space> を使う。
//
// req: FR-97
func TestLeaderKeysInvalid(t *testing.T) {
	for _, bad := range []string{"<Leader>", "<Foo>", "q", "1", `"`, "@", ".", "<Esc>", "<CR>"} {
		keys, err := leaderKeys(bad)
		if err == nil || editcmd.KeysString(keys) != "<Space>" {
			t.Errorf("%q: keys = %q, err = %v", bad, editcmd.KeysString(keys), err)
		}
	}
	h := start(t, Config{Leader: "<Foo>"})
	if got := h.message(); !strings.HasPrefix(got, "leader: ") {
		t.Errorf("message = %q", got)
	}
}

// 拡張機能の宣言ファイルの keys の <Leader> も leader に置き換え、leader を変えれば追従する。
//
// req: FR-97
func TestBuildKeymapLeader(t *testing.T) {
	specs := []exthost.Spec{{Manifest: ext.Manifest{ID: "lx", Contributes: ext.Contributes{
		// <Leader>lh は本体の LSP の操作（FR-95）の既定のキーなので、重ならないキーにする。
		Commands: []ext.Contribution{{ID: "lx.hover", Keys: "<Leader>xh"}},
	}}}}
	for _, leader := range []string{"<Space>", ","} {
		km, errs := buildKeymap(specs, editcmd.MustParseKeys(leader))
		if len(errs) != 0 {
			t.Fatalf("errs = %v", errs)
		}
		b, ok := km.Lookup(input.MapNormal, editcmd.MustParseKeys(leader+"xh"))
		if !ok || b.Ref.ID != "lx.hover" || b.Ref.Extension != "lx" {
			t.Errorf("leader %q: %sxh = %+v, %v", leader, leader, b, ok)
		}
	}
}

// 宣言ファイルの overrideKeys のキーは、keys に加えて割り当て、本体の既定のキー（<C-p> の貼り直し）と重なれば本体のキーを外す。
// 外した本体のキーは、設定の keymap が外したものと違い、キーの一覧に残さない。keys は今までどおり本体のキーと重なれば割り当てない。
// 拡張機能どうしの重なりは、overrideKeys でも割り当てずに誤りにする（宣言の順に依らない）。
// 設定の keymap は overrideKeys より優先する。
func TestBuildKeymapOverrideKeys(t *testing.T) {
	specs := []exthost.Spec{
		{Manifest: ext.Manifest{ID: "fx", Contributes: ext.Contributes{Commands: []ext.Contribution{
			{ID: "fx.files", Keys: "<Leader>ff", OverrideKeys: []string{"<C-p>", "<Leader>zz"}},
			{ID: "fx.bb", OverrideKeys: []string{"<C-b>"}},
			{ID: "fx.next", Keys: "<C-n>"},
		}}}},
		{Manifest: ext.Manifest{ID: "gx", Contributes: ext.Contributes{Commands: []ext.Contribution{
			{ID: "gx.zz", Keys: "<Leader>zz"},
			{ID: "gx.bb", Keys: "<C-b>"},
		}}}},
	}
	km, errs := buildKeymap(specs, editcmd.MustParseKeys("<Space>"))
	// keys の <C-n> は本体の貼り直しと重なるので割り当てない。overrideKeys の <Space>zz と <C-b> は gx の keys と重なる。
	if len(errs) != 3 || !strings.Contains(errs[0].Error(), "fx.next（<C-n>）") ||
		!strings.Contains(errs[1].Error(), "fx.files（<Leader>zz）") || !strings.Contains(errs[2].Error(), "fx.bb（<C-b>）") {
		t.Errorf("errs = %v", errs)
	}
	for keys, want := range map[string]string{"<C-p>": "fx/fx.files", "<Space>ff": "fx/fx.files", "<Space>zz": "gx/gx.zz", "<C-b>": "gx/gx.bb", "<C-n>": "/put-newer"} {
		b, ok := km.Lookup(input.MapNormal, editcmd.MustParseKeys(keys))
		if got := b.Ref.Extension + "/" + b.Ref.ID; !ok || got != want {
			t.Errorf("%s = %s (%v), want %s", keys, got, ok, want)
		}
	}
	for _, b := range append(km.Bindings(), km.Overridden()...) {
		if b.Ref.ID == "put-older" {
			t.Errorf("put-older remains: %+v", b)
		}
	}

	// 設定の keymap は、拡張機能の overrideKeys より優先する。
	finder := []exthost.Spec{specs[0]}
	finder[0].Manifest.Command = "/bin/true"
	h := start(t, Config{Extensions: finder, KeymapActions: map[string]map[string]string{"normal": {"<C-p>": "put-older"}}})
	var b input.Binding
	var ok bool
	h.e.do(func() { b, ok = h.e.keymap.Lookup(input.MapNormal, editcmd.MustParseKeys("<C-p>")) })
	if !ok || b.Ref.ID != "put-older" || !b.User {
		t.Errorf("<C-p> with the user keymap = %+v, %v", b, ok)
	}
}

// 設定の leader とキーマップの <Leader> を、起動したエディタで使える。
//
// req: FR-97
func TestUserKeymapLeader(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	h := start(t, Config{
		WorkDir: dir,
		Leader:  ",",
		Keymap:  map[string]map[string]string{"normal": {"<Leader>d": "dd"}},
	}, "a.txt")
	if got := editcmd.KeysString(h.e.leader); got != "," {
		t.Errorf("leader = %q", got)
	}
	h.keys(",d")
	h.settle()
	if got := h.text(); got != "two\nthree" {
		t.Errorf(",d: text = %q", got)
	}
}

// 左辺の表記が違っても置き換えた後のキーの列が同じになる項目は、表記をバイトの順に並べた最初の項目を使い、残りを誤りにする。
// 何度作っても同じ項目が効く（Go の map の順に依らない）。
//
// req: FR-97
func TestRemapperDuplicateLHS(t *testing.T) {
	keymap := map[string]map[string]string{
		"normal": {"<leader>w": "yy", "<Space>w": "x", "<Leader>w": "dd", "<space>w": "p"},
	}
	for range 50 {
		r, errs := newRemapper(keymap, nil)
		if len(errs) != 3 {
			t.Fatalf("errs = %v", errs)
		}
		for _, err := range errs {
			if !strings.Contains(err.Error(), `と同じキーの列 <Space>w になるので、"<Leader>w" を使う`) {
				t.Errorf("err = %v", err)
			}
		}
		var out []editcmd.Key
		for _, k := range editcmd.MustParseKeys("<Space>w") {
			out = append(out, r.feed(mapNormal, k)...)
		}
		if got := editcmd.KeysString(out); got != "dd" {
			t.Fatalf("<Space>w → %q", got)
		}
	}
}

// 利用者のキーマップの展開。接頭辞を溜め、進めなくなったら確定させる。展開した列は展開し直さない。
//
// req: FR-41
func TestRemapper(t *testing.T) {
	r, errs := newRemapper(map[string]map[string]string{
		"normal": {"ab": "X", "abc": "Y", "Q": "ab", "<Foo>": "x", "<F5>": "dd", "<Insert>": "i"},
		"nomode": {"a": "b"},
	}, nil)
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

package editcmd

import "testing"

// <Leader> は大文字と小文字を区別せずに leader へ置き換え、leader が空なら <Space> を使う。
//
// req: FR-97
func TestParseKeysLeader(t *testing.T) {
	comma := MustParseKeys(",")
	tests := []struct {
		in     string
		leader []Key
		want   string
	}{
		{"<Leader>w", nil, "<Space>w"},
		{"<leader>w", comma, ",w"},
		{"<LEADER>l<Leader>", comma, ",l,"},
		{"d<Leader>", MustParseKeys("<C-x>a"), "d<C-x>a"},
		{"<<Leader>", comma, "<lt>,"},
		{"<lt>Leader>", comma, "<lt>Leader>"},
		{"<Space>w", comma, "<Space>w"},
		{"", comma, ""},
	}
	for _, tt := range tests {
		got, err := ParseKeysLeader(tt.in, tt.leader)
		if err != nil {
			t.Errorf("%q: %v", tt.in, err)
			continue
		}
		if s := KeysString(got); s != tt.want {
			t.Errorf("%q: got %q, want %q", tt.in, s, tt.want)
		}
	}
	// "<lt>Leader>" は '<' と 7 文字で、leader ではない。
	if got, _ := ParseKeysLeader("<lt>Leader>", comma); len(got) != 8 {
		t.Errorf("<lt>Leader>: %d 個", len(got))
	}
	for _, bad := range []string{"<Leader><Foo>", "<Foo><Leader>"} {
		if _, err := ParseKeysLeader(bad, comma); err == nil {
			t.Errorf("%q: 誤りにならない", bad)
		}
	}
}

// 設定の leader は空でないキーの列にし、<Leader> を含めない。
//
// req: FR-97
func TestParseLeader(t *testing.T) {
	for _, ok := range []string{"<Space>", ",", `\`, "<C-k>", ",,", "0", "<C-q>", "<A-1>", ",q", "<S-CR>", "<Tab>"} {
		if _, err := ParseLeader(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	// パーサーの先に読むキー（回数、レジスター、マクロ、ドットリピート）と、モードの操作に使う <Esc> と <CR> で始まる値も誤りにする。
	for _, bad := range []string{"", "<Leader>", "a<leader>", "<Foo>", "1", "9x", `"`, "q", "@", ".", "<Esc>", "<CR>", "<Esc>,"} {
		if _, err := ParseLeader(bad); err == nil {
			t.Errorf("%q: 誤りにならない", bad)
		}
	}
}

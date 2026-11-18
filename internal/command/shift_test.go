package command

import "testing"

// shiftCases は、ノーマルモードの > と <（FR-76）と、ビジュアルモードの > と <（FR-77）のケースである。
//
// 期待値の形と入力の前のレジスターは vimCase と同じである。Vim 9.1 を Ex モードでない起動
// （`vim -Nu NONE -n --not-a-term`）で動かし、nnoremap > >>、nnoremap < <<、xnoremap > >gv、xnoremap < <gv を割り当てて、
// ケースごとに setline() の後でアンドゥの履歴を消してから、keys を feedkeys(keys, "xt") で入力した。
var shiftCases = []vimCase{
	// ノーマルモードの > と < は次のキーを待たないので、>> と << は 2 回ずらす（#177 のコメントの決定）。
	{"a|b\ncd", ">>", "\t\tab\ncd", "0:2", "v:U", "v:U", ":", ":", "v:A"},
	{"\t\t\ta|b", "<lt><lt>", "\tab", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	// 回数は行数である。
	{"a|b\ncd\nef", "3>", "\tab\n\tcd\n\tef", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a\nb|", "3>", "a\nb", "1:0", "v:U", "v:U", ":", ":", "v:A"},
	// ドットリピートは同じ行数をずらし、回数を付けた . は行数を置き換える。
	{"a|b\ncd\nef\ngh", "2>j.", "\tab\n\t\tcd\n\tef\ngh", "1:2", "v:U", "v:U", ":", ":", "v:A"},
	{"a|b\ncd\nef\ngh", ">j3.", "\tab\n\tcd\n\tef\n\tgh", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"\t\ta|b\ncd", "<lt>.u", "\tab\ncd", "0:1", "v:U", "v:U", ":", ":", "v:A"},
	{"|a\nb\nc", "qa>jq2@a", "\ta\n\tb\n\tc", "2:1", "v:U", "v:U", ":", ":", "v:>j"},
	// ビジュアルモードの > と < は、ビジュアルモードと選択を保つので、続けてずらしたり、選択に別のオペレーターを働かせたりできる。
	{"a|bc\nde\nf", "vj>>><Esc>", "\t\t\tabc\n\t\t\tde\nf", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nde\nf", "Vj><lt><Esc>", "abc\nde\nf", "1:1", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nde\nf", "vj>d", "\te\nf", "0:1", "v:abc\n\td", "v:U", "v:abc\n\td", ":", "v:A"},
	{"a|bc\nde\nf", "Vj>j><Esc>", "\t\tabc\n\t\tde\n\tf", "2:1", "v:U", "v:U", ":", ":", "v:A"},
	{"\t\ta|bc\nde", "v<lt>l<Esc>", "\tabc\nde", "0:3", "v:U", "v:U", ":", ":", "v:A"},
	{"a|bc\nde\nf", "vj>o<Esc>", "\tabc\n\tde\nf", "0:1", "v:U", "v:U", ":", ":", "v:A"},
}

// ノーマルモードの > と < は、Vim の >> と << と同じく行をずらし、ビジュアルモードの > と < は選択を保つ。
//
// req: FR-76, FR-77
func TestShift(t *testing.T) {
	runVimCases(t, shiftCases)
}

// ビジュアルモードの > と < の後も、モードと選択の両端が変わらない。
//
// req: FR-77
func TestVisualShiftKeepsSelection(t *testing.T) {
	tests := []struct {
		text, keys string
		mode       Mode
		start, end string
	}{
		{"a|bc\nde\nf", "vj>", ModeVisual, "0:1", "1:1"},
		{"a|bc\nde\nf", "Vj3>", ModeVisualLine, "0:1", "1:1"},
		{"\t\ta|bc\nde", "vj<lt>", ModeVisual, "0:3", "1:2"},
	}
	for _, tt := range tests {
		t.Run(tt.text+"/"+tt.keys, func(t *testing.T) {
			s := setup(t, tt.text)
			feedKeys(s, tt.keys)
			start, end, _, ok := s.Selection()
			if !ok || s.Mode() != tt.mode {
				t.Fatalf("mode = %v, want %v", s.Mode(), tt.mode)
			}
			if start.String() != tt.start || end.String() != tt.end {
				t.Errorf("selection = %s-%s, want %s-%s", start, end, tt.start, tt.end)
			}
		})
	}
}

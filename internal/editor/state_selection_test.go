package editor

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/input"
)

// editor/state の選択は、文字単位の選択の終わりが行末の後（Vim の NUL の位置）なら、その行の改行まで含み、
// d が消す範囲と一致する（#89）。最後の行では改行を含めない。
func TestEditorStateSelectionEOL(t *testing.T) {
	for _, tc := range []struct {
		text, keys string
		want       ext.Range
	}{
		// ab|c、d、e の vj は、c、d、改行を選ぶ（d で abe になる）。
		{"abc\nd\ne\n", "llvj", ext.Range{Start: ext.Position{Line: 0, Byte: 2}, End: ext.Position{Line: 2, Byte: 0}}},
		// 最後の行の行末の後なら、行末まで。
		{"abc\nd\n", "llvj", ext.Range{Start: ext.Position{Line: 0, Byte: 2}, End: ext.Position{Line: 1, Byte: 1}}},
		// 終わりが最後の文字なら、その文字まで。
		{"abc\nde\n", "vj", ext.Range{Start: ext.Position{Line: 0, Byte: 0}, End: ext.Position{Line: 1, Byte: 1}}},
	} {
		t.Run(tc.keys+"/"+tc.text, func(t *testing.T) {
			buf := buffer.New(tc.text)
			bs := &bufferState{id: 1, buf: buf, state: command.New(buf, input.NewParser(input.DefaultKeymap()))}
			e := &Editor{bufs: []*bufferState{bs}, cur: bs}
			for _, k := range editcmd.MustParseKeys(tc.keys) {
				bs.state.Feed(k)
			}
			res := e.editorState()
			if len(res.Selections) != 1 || res.Selections[0] != tc.want {
				t.Errorf("selections = %+v, want %+v", res.Selections, tc.want)
			}
		})
	}
}

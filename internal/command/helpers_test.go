package command

import (
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// newBuffer は、text の行をそのまま行とするバッファを作る（Vim の setline(1, split(text, "\n", 1)) と同じ行になる）。
func newBuffer(text string) *buffer.Buffer { return buffer.New(text + "\n") }

// feedKeys は、Vim の表記のキーの列を 1 個ずつ Feed し、結果を全て返す。
func feedKeys(s *State, keys string) []Result {
	var out []Result
	for _, k := range input.MustParseKeys(keys) {
		out = append(out, s.Feed(k))
	}
	return out
}

package command

import (
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// actDiff は、見比べの操作（]c、[c、do、dp。基本設計 6.8。FR-101）である。
// 相手のバッファと差分は呼び出し側（internal/editor）が持つので、Navigator に ex.ActionDiff で頼む。
// ]c と [c は、移る先の位置を Navigator が返し、State がカーソルを動かす（Vim と同じくジャンプリストには記録しない）。
// do と dp は、Navigator がバッファを編集する（アンドゥの 1 単位）。ドットリピートには記録しない。
//
// req: FR-101
func actDiff(op ex.DiffOp) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		return s.navigate(ex.Action{Kind: ex.ActionDiff, Diff: op, Count: max(cmd.Count, 1)})
	}
}

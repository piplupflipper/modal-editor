package command

import (
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// actOutput は、出力の欄（基本設計 6.7）を操作するキーの操作である。
// 欄は呼び出し側（internal/editor）が持つので、:output と同じ ex.ActionOutput の動作を Result.Actions で返す。
// バッファを変えないので、ドットリピートとアンドゥには関わらない。回数は使わない。
func actOutput(op ex.OutputOp) actionFunc {
	return func(s *State, _ editcmd.Command) error {
		s.res.Actions = append(s.res.Actions, ex.Action{Kind: ex.ActionOutput, Output: op})
		return nil
	}
}

package command

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// Navigator は、バッファをまたぐ移動（ジャンプリストとバッファの切り替え）の口である。
// ジャンプリストとバッファの一覧は、すべてのバッファを持つ呼び出し側（internal/editor）が持つので、State は口を通して頼む。
// nil なら、ジャンプは記録せず、<C-o>、<C-i>、<C-^> は失敗する。
type Navigator interface {
	// MarkJump は、ジャンプする前のカーソルの位置 from を、このバッファの位置としてジャンプリストに記録する（Vim 9.1 の setpcmark）。
	MarkJump(from buffer.Position)
	// Navigate は、a（ex.ActionJump か、ex.ActionBuffer の BufferAlternate、BufferNext、BufferPrev）の移動を行う。cursor は現在のカーソルである。
	// 移動先がこのバッファの中なら、移す位置と true を返し、State がカーソルを動かす。
	// 別のバッファへ移したら ErrBufferSwitched を返す。State は操作をそこで終え、マクロの再生の残りは移った先のバッファで続く。
	// 移動先が無ければ ErrFailed を包んだエラーを、未保存の変更などで移れなければ画面に出す誤りを返す。
	Navigate(a ex.Action, cursor buffer.Position) (to buffer.Position, moved bool, err error)
	// RunActions は、マクロの再生の途中の Ex コマンドが求めたエディタの動作 acts（:w、:e、:b、:bn など）を、その場で先頭から順に行う。
	// 再生の残りのキーを、動作の後の状態（移った先のバッファ、書き出した後のファイル）で動かすためである（Vim と同じ）。
	// 別のバッファへ移したら ErrBufferSwitched を返す。State は操作をそこで終え、マクロの再生の残りは移った先のバッファで続く。
	// 動作が失敗したら、残りの動作は行わずに、その誤りを返す（画面に出すのは呼び出し側である）。
	RunActions(acts []ex.Action) error
}

// jumpMotions は、ジャンプに当たる本体のモーションである（Vim 9.1 の jump-motions のうち、実装しているもの）。
// 移動が成り立てば、移る前のカーソルの位置をジャンプリストに記録する。オペレーターの対象とビジュアルモードの移動でも記録する。
// 移る先が同じ位置（1 行目での gg）でも記録する。Vim 9.1 で確かめた。
var jumpMotions = map[string]bool{
	"file-start":      true,
	"file-end":        true,
	"search-forward":  true,
	"search-backward": true,
	"search-next":     true,
	"search-prev":     true,
	"match-pair":      true,
	"paragraph-prev":  true,
	"paragraph-next":  true,
}

// markJump は、現在のカーソルの位置をジャンプリストに記録する。
func (s *State) markJump() {
	if s.Navigator != nil {
		s.Navigator.MarkJump(s.cursor)
	}
}

// actJump は、<C-o>（dir が -1）と <C-i>（dir が 1）である。ジャンプリストを回数だけ戻るか進む（Vim 9.1 の CTRL-O と CTRL-I）。
// ジャンプリストとバッファの切り替えのキーは要件定義の FR のどれにも当たらないので、req: の注釈は付けない。
func actJump(dir int) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		return s.navigate(ex.Action{Kind: ex.ActionJump, Count: dir * max(cmd.Count, 1)})
	}
}

// actAlternate は、<C-^> である。裏のバッファへ移る。回数を付けると、その番号のバッファへ移る（Vim の N CTRL-^）。
// 未保存の変更があれば、Vim の 'hidden' がオフの既定と同じく E37 で移らない。
func actAlternate(s *State, cmd editcmd.Command) error {
	a := ex.Action{Kind: ex.ActionBuffer, Buffer: ex.BufferAlternate}
	if cmd.CountGiven {
		a.Count = cmd.Count
	}
	return s.navigate(a)
}

// actTab は、<Tab>（dir が 1）と <S-Tab>（dir が -1）である。client の中の次と前のタブへ、回数だけ移る（FR-98）。
// 端の次は反対の端へ回る（:bn と :bp と同じ。基本設計 4.4 の段階 3 のタブとバッファ）。
//
// req: FR-98
func actTab(dir int) actionFunc {
	return func(s *State, cmd editcmd.Command) error {
		op := ex.BufferNext
		if dir < 0 {
			op = ex.BufferPrev
		}
		return s.navigate(ex.Action{Kind: ex.ActionBuffer, Buffer: op, Count: max(cmd.Count, 1)})
	}
}

// navigate は、Navigator に移動を頼み、同じバッファの中の移動ならカーソルを動かす。
func (s *State) navigate(a ex.Action) error {
	if s.Navigator == nil {
		return failf("no navigator")
	}
	to, moved, err := s.Navigator.Navigate(a, s.cursor)
	if err != nil {
		return err
	}
	if moved {
		s.setCursorNormal(to)
	}
	return nil
}

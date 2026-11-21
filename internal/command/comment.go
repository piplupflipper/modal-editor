package command

import (
	"errors"
	"fmt"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/comment"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// コメントの切り替え（FR-169。基本設計 3.3）。
//
// オペレーターの gc（ビジュアルモードの gc と <C-_>）、その省略形の gcc（comment-line。ノーマルモードの <C-_>）、
// オペレーターの後の gc（テキストオブジェクトの commented-lines。internal/command/target）の 3 つがある。
// 振る舞いは Neovim 0.12.5 の組み込みの gc（runtime/lua/vim/_comment.lua）に合わせた。行の並びの切り替え方は internal/command/comment にある。
// 期待値は 2026-11-21 に手元の Neovim 0.12.5（nvim --clean）で取った（comment_cases_test.go）。

// commentEmptyMessage は、コメントの形が空のときの警告である（Neovim の "Option 'commentstring' is empty."）。
const commentEmptyMessage = "comment.string が空です"

// commentString は、位置 at で使う行のコメントの形を返す（target.Request.CommentString）。
// コメントの形を決めるのはここだけである。今はバッファの設定 comment.string の今の値を、Ex.Options（internal/editor が差し込む設定の口）から読む。
// 口が無ければ（internal/command のテスト）、表の既定値（空）を使う。形が空なら、Neovim と同じく警告を Result.Message に入れる。
//
// at は、後に tree-sitter の拡張機能でカーソルの位置の言語の形（Markdown のコードの囲みの中の言語など）を使うときのために受け取る。
// Neovim はオペレーターではカーソルの位置（範囲の始まり）、テキストオブジェクトではカーソルの位置の言語で決める。
// ここはイベントループの上から同期に呼ばれ、拡張機能の応答を待てないので、そのときは言語の範囲を前もって本体に持ち、
// 別の種類の形を fileTypeOptions まで含めて決める口を internal/editor から差し込む（基本設計 3.3。PR #331 のレビューの推奨 1）。
//
// req: FR-169
func (s *State) commentString(at buffer.Position) string {
	_ = at
	d, _ := option.Lookup(option.CommentString)
	v := d.Default
	if s.Ex.Options != nil {
		v = s.Ex.Options.Get(d)
	}
	if v.String() == "" {
		s.res.Message = Message{Text: commentEmptyMessage, Warning: true}
	}
	return v.String()
}

// opComment は、gc である。範囲の行のコメントを切り替える（comment.Parts.Toggle）。
// 範囲は行で数え、文字単位の範囲も、始まりの行から終わりの文字の行までを切り替える（Neovim の '[ と '] の行）。
// 排他的な範囲が次の行の先頭で終わる（gc} や、ビジュアルモードで行末の改行まで選んだ）ときは、その行を含めない。空の範囲（行頭の gc0）も、その行を切り替える。
//
// コメントの形が空なら、Neovim と同じく警告を出し、前と後ろを空として続ける（空白だけの行が空の行になるほかは変わらない）。
// 変わった行だけを書き換える。Neovim は範囲の行をすべて書き直すので、何も変わらなくてもバッファを変更ありにするが、本体は変更ありにしない。
// カーソルは範囲の始まり（gcc では行の最初の空白でない文字。gcj ではカーソルの位置）のバイトの位置に置く（Neovim と同じく、付けた記号の分ずらさない）。
//
// req: FR-169
func opComment(s *State, _ editcmd.Command, rg region, _ opOptions) error {
	first, last := rg.r.Start.Line, rg.r.End.Line
	if !rg.linewise && rg.r.End.Byte == 0 && last > first {
		last--
	}
	parts, err := comment.Parse(s.commentString(rg.start))
	if err != nil && !errors.Is(err, comment.ErrEmpty) {
		return fmt.Errorf("comment.string の値が正しくない: %w", err)
	}
	lines := make([]string, 0, last-first+1)
	for l := first; l <= last; l++ {
		lines = append(lines, s.line(l))
	}
	toggled, err := parts.Toggle(lines)
	if err != nil {
		return fmt.Errorf("comment.string の値が正しくない: %w", err)
	}
	var edits []buffer.Edit
	for i, text := range toggled {
		if text != lines[i] {
			l := first + i
			edits = append(edits, buffer.Edit{Range: buffer.Range{Start: pos(l, 0), End: pos(l, len(lines[i]))}, Text: text})
		}
	}
	if err := s.apply(edits...); err != nil {
		return err
	}
	s.setCursorNormal(rg.start)
	return nil
}

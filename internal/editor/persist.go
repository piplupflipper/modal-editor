package editor

import (
	"crypto/sha256"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/persist"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// アンドゥの履歴とカーソルの位置を、エディタを終えた後も残す（FR-165、FR-166。基本設計 4.10）。
// 置き場は Config.StateDir で、ファイルの読み書きは internal/persist が、履歴のバイト列は internal/buffer が受け持つ。
// ここでは、いつ読み、いつ書くかと、どのバッファを残すかを決める。

// undoPersist は、アンドゥの履歴を残すか（設定の undo.persist。置き場が決まっているときだけ）を返す。
func (e *Editor) undoPersist() bool {
	return e.cfg.StateDir != "" && e.options.Get(option.UndoPersist).Bool()
}

// cursorRestore は、カーソルの位置を残すか（設定の cursor.restore。置き場が決まっているときだけ）を返す。
func (e *Editor) cursorRestore() bool {
	return e.cfg.StateDir != "" && e.options.Get(option.CursorRestore).Bool()
}

// persistable は、bs の履歴とカーソルの位置を残すかを返す。ファイルのバッファだけを残し、
// 名前の無いバッファ（標準入力から読んだバッファ。FR-158）、ファイルでないバッファ（:help、拡張機能の buffer/create）、
// ポップアップのバッファ（プレビューなど）は残さない。
func persistable(bs *bufferState) bool {
	return bs.path != "" && bs.title == "" && bs.creator == "" && !bs.popup
}

// restoreUndo は、開いたファイル path の中身 data で作ったバッファ b へ、残したアンドゥの履歴を読み戻す（FR-165）。
// 履歴を書いた後にファイルの中身が外で変わっていれば（ハッシュが違えば）、形式が違えば、履歴が中身に当てはまらなければ、読まずに捨てる。
// Vim 9.1 と同じく、捨てたことは知らせない（Vim は 'verbose' を付けたときだけ出す）。
//
// req: FR-165
func (e *Editor) restoreUndo(b *buffer.Buffer, path string, data []byte) {
	if !e.undoPersist() {
		return
	}
	history, err := persist.ReadUndo(e.cfg.StateDir, path, sha256.Sum256(data))
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			e.log.Info("アンドゥの履歴を読まない", "path", path, "err", err)
		}
		return
	}
	if err := b.RestoreHistory(history); err != nil {
		e.log.Info("アンドゥの履歴を読み戻せない", "path", path, "err", err)
	}
}

// saveUndo は、ファイルへ保存した bs のアンドゥの履歴を残す（FR-165）。保存したファイルの中身のハッシュと組にして書く。
// 履歴が無ければ、古い履歴のファイルを消す（Vim 9.1 と Neovim 0.12.5 と同じ）。
// 書けなければ、メッセージの行に添える文を返す（保存そのものは失敗にしない）。書けたか、残さないときは空を返す。
//
// req: FR-165
func (e *Editor) saveUndo(bs *bufferState) string {
	if !e.undoPersist() || !persistable(bs) {
		return ""
	}
	var err error
	if bs.buf.CanUndo() || bs.buf.CanRedo() {
		h := sha256.New()
		if _, err = bs.buf.WriteTo(h); err == nil {
			err = persist.WriteUndo(e.cfg.StateDir, bs.path, persist.Sum(h.Sum(nil)), bs.buf.MarshalHistory())
		}
	} else {
		err = persist.RemoveUndo(e.cfg.StateDir, bs.path)
	}
	if err != nil {
		e.log.Warn("アンドゥの履歴を残せない", "path", bs.path, "err", err)
		return "アンドゥの履歴を残せません: " + err.Error()
	}
	return ""
}

// cursorExcludedFileTypes は、カーソルの位置を戻さないファイルの種類である。Vim 9.1 の defaults.vim の BufReadPost の自動コマンドが
// 除くもの（'filetype' に commit を含むもの、xxd、gitrebase、tutor）と同じにした。
// コミットのメッセージは毎回違う中身なので、前の位置へ戻さない。
var cursorExcludedFileTypes = []string{"xxd", "gitrebase", "tutor"}

// restoreCursor は、開いたファイルのバッファ bs のカーソルを、前に閉じたときの位置へ置く（FR-166）。
// 動きは Vim 9.1 の defaults.vim の BufReadPost の自動コマンド（g`" で '" の印へ動く）と同じにする。
//
//   - 残した位置の行が今の行数を超えるときは、動かさない（1 行目のまま。defaults.vim の line("'\"") <= line("$") の条件）
//   - 列が行の長さを超えるときは、行の最後の文字へ寄せる（g`" と同じ）
//   - ファイルの種類が commit を含むもの（gitcommit）と cursorExcludedFileTypes では戻さない
//
// 起動の引数や拡張機能の editor/open で位置を指定して開くときは、呼び出し側がこの後にカーソルを置き直すので、指定した位置になる。
// ジャンプリストには記録しない（g`" と同じ）。位置へ置いたら true を返す。
//
// req: FR-166
func (e *Editor) restoreCursor(bs *bufferState) bool {
	if !e.cursorRestore() || !persistable(bs) {
		return false
	}
	ft := bs.fileType()
	if strings.Contains(ft, "commit") || slices.Contains(cursorExcludedFileTypes, ft) {
		return false
	}
	p, ok, err := persist.LoadCursor(e.cfg.StateDir, bs.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		e.log.Info("カーソルの位置を読めない", "err", err)
	}
	if !ok || p.Line >= bs.buf.LineCount() {
		return false
	}
	bs.state.SetCursor(buffer.Position{Line: p.Line, Byte: p.Byte})
	return true
}

// saveCursor は、閉じるファイルのバッファ bs のカーソルの位置を残す（FR-166）。saveCursors の 1 つの形である。
//
// req: FR-166
func (e *Editor) saveCursor(bs *bufferState) {
	e.saveCursors([]*bufferState{bs})
}

// saveCursors は、閉じるファイルのバッファ bufs のカーソルの位置を、状態のファイルの 1 回の書き換えでまとめて残す（FR-166）。
// server が終わるとき（Editor.Close）に、バッファの数だけロックと書き換えを繰り返さないためにまとめる。
// 画面に出さないバッファ（FR-109）は、利用者がカーソルを動かしていないので残さない。
//
// 書けなくても閉じることは止めず、メッセージの行に警告を出してログに記録する（最後のタブを閉じて client が終わるときは、警告は画面に残らない）。
// 新しい版の本体が書いた項目（persist.ErrNewerItem）で書けないときは、利用者には直せないので、警告を出さずにログだけに残す。
//
// req: FR-166
func (e *Editor) saveCursors(bufs []*bufferState) {
	if !e.cursorRestore() {
		return
	}
	var cs []persist.Cursor
	for _, bs := range bufs {
		if !persistable(bs) || bs.offscreen {
			continue
		}
		c := bs.state.Cursor()
		cs = append(cs, persist.Cursor{Path: bs.path, Pos: persist.Position{Line: c.Line, Byte: c.Byte}})
	}
	err := persist.SaveCursors(e.cfg.StateDir, cs, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, persist.ErrNewerItem):
		e.log.Info("新しい版の本体が書いたカーソルの位置の項目なので、残さない", "err", err)
	default:
		e.log.Warn("カーソルの位置を残せない", "err", err)
		e.message = ui.Message{Text: "カーソルの位置を残せません: " + err.Error(), Level: ui.MessageWarning}
	}
}

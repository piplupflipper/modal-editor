package editor

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// 画面に出さないバッファ（FR-109。基本設計 4.4 の「画面に出さないバッファ」）。
//
// 拡張機能が、どのタブにもなっていないファイルを editor/open（focus が false）で開いたら、タブを増やさずに開く
// （bufferState.offscreen。editorOpenRequest）。拡張機能の手順は段階 2 までと変わらず、buffer/edit で編集し、buffer/save で書き出す。
// 書き出したら、そのバッファをすぐ閉じて拡張機能へ buffer/close を送り、変えたファイルの一覧をメッセージの行に出す（closeOffscreen）。
// 利用者が :e などでタブにしたら、ふつうのタブのバッファになり、保存の後に閉じない（addTab が offscreen を下ろす）。
// buffer/save が来ないまま残ったバッファの扱いは未決（基本設計 10 章）で、どのタブでもないバッファとして残る。

// offscreenNote は、client のメッセージの行に出している、画面に出さないバッファを閉じた知らせである。
// 同じ拡張機能が続けて閉じたら、知らせが消されていなければ、同じ行に足す（closeOffscreen）。
type offscreenNote struct {
	ext   string
	names []string
	// dirs は、書き出すときに作ったディレクトリの数である（FR-78）。
	dirs int
	msg  ui.Message
}

// sameAsFile は、bs の中身が、ファイル bs.path の今の中身と同じかを返す。ファイルが無いか読めなければ false を返す。
// 画面に出さないバッファへの buffer/save で、拡張機能が編集を元に戻したバッファを書き出さずに閉じるのに使う（bufferSaveRequest）。
func sameAsFile(bs *bufferState) bool {
	if !bs.disk.exists {
		return false
	}
	b, err := buffer.ReadFile(bs.path)
	return err == nil && sameContent(bs.buf, b)
}

// closeOffscreen は、拡張機能 extID の buffer/save で書き出した画面に出さないバッファ bs を閉じ、
// 変えたファイルの一覧を、その拡張機能へ問い合わせを始めた client（extView）のメッセージの行に出す（FR-109）。
//
// 一覧は「拡張機能 X が N 個のファイルを変えました: a.go、b.go」の 1 行にする（文言は仮。基本設計 4.4）。
// 前に出した同じ拡張機能の知らせがメッセージの行に残っていれば、その行に足す（ACP のエージェントが続けて書いたファイルを 1 行にまとめる）。
// 画面の幅に収まらなければ、件数だけを出す。本体の誤りが出ている間は、拡張機能のメッセージと同じく上書きしない（showExtMessage）。
// made は、書き出すときに作ったディレクトリ（FR-78）で、空でなければファイル名に「（ディレクトリ "…" を作りました）」を添える。
// 件数だけを出すときは、作ったディレクトリの数を添える（作った知らせそのものは、呼び出し側がメッセージの履歴に残す）。
//
// req: FR-78, FR-109
func (e *Editor) closeOffscreen(extID string, bs *bufferState, made string) {
	name := filepath.Base(bs.path)
	e.closeBufferState(bs)
	e.withView(e.extView(extID), func() {
		if made != "" {
			// 作業ディレクトリの下なら、相対のパスで出す（shortName と同じ）。
			if rel, err := filepath.Rel(e.workDir(), made); err == nil && filepath.IsLocal(rel) {
				made = rel
			}
			name += fmt.Sprintf("（ディレクトリ \"%s\" を作りました）", made)
		}
		n := e.offscreenNote
		if n.ext != extID || e.message != n.msg || n.msg.Text == "" {
			n = offscreenNote{ext: extID}
		} else if k := len(e.msgHistory); k > 0 && e.msgHistory[k-1] == n.msg {
			// 足す前の知らせは、履歴でも足した後のもので置き換える。
			e.msgHistory = e.msgHistory[:k-1]
		}
		n.names = append(slices.Clone(n.names), name)
		if made != "" {
			n.dirs++
		}
		text := fmt.Sprintf("拡張機能 %s が %d 個のファイルを変えました: %s", extID, len(n.names), strings.Join(n.names, "、"))
		if ui.StringWidth(text, 1) > e.renderer.Width {
			text = fmt.Sprintf("拡張機能 %s が %d 個のファイルを変えました", extID, len(n.names))
			if n.dirs > 0 {
				text += fmt.Sprintf("（ディレクトリを %d 個作りました。:messages）", n.dirs)
			}
		}
		n.msg = infoMessage(text)
		e.showExtMessage(n.msg)
		if e.message == n.msg {
			e.offscreenNote = n
		}
	})
}

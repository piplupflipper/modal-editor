package editor

import (
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 決まった時間が過ぎたら消える色付け（基本設計 6.7 の「一時的な色付け」。FR-140）。
//
// 色付けは、1 つのバッファの範囲の一覧と色の名前の組で、本体の全体で 1 つだけ持つ（Editor.flash）。
// 新しく付けると、どのバッファのものでも前の色付けを外す（nvim 0.12.5 の vim.hl.on_yank が、タイマーと消す関数をエディタの全体で
// 1 つだけ持ち、前のヤンクの色を消してから付けるのと同じ。PR #287 のレビューの推奨 2）。
// 使う側（ヤンクした範囲の色の flashYank）は addFlash で付けるだけで、消すのはこのファイルの中の規則による。
//
//   - 時間が過ぎたら消す。時間の待ちはイベントループの外のタイマー（Editor.afterFunc）で行い、時間が来たら inspect で
//     イベントループに戻して外す（基本設計 2.1。編集の状態はイベントループの goroutine だけが触る）。外した後は、
//     inspect の後の描き直し（Run の renderIdle）で画面から消える
//   - バッファが変わったら描かない。付けたときのバッファとバージョン番号を覚えておき、描くとき（flashHighlights）に今のものと
//     違えば一覧に入れない。バージョン番号は、本体の編集、拡張機能の編集、アンドゥ、リドゥ、読み直しのたびに進む（基本設計 4.2）ので、
//     変更の経路ごとに消す処理は無い。位置をずらして残すことはしない
//   - 新しく付けたら、前の色付けを外してそのタイマーを止める

// flash は、決まった時間が過ぎたら消える色付けである。
type flash struct {
	// bs は、色を付けるバッファである。描くのは、このバッファをタブに出している画面だけである。
	bs *bufferState
	// buf と version は、色付けの範囲が指すテキストのバッファとバージョン番号である。今のものと違えば描かない。
	buf     *buffer.Buffer
	version uint64
	// ranges は、色を付ける範囲の一覧である。
	ranges []ui.Highlight
	// stop は、時間が来たら外すタイマーを止める。
	stop func() bool
}

// afterFunc は、Editor.afterFunc の既定で、time.AfterFunc を掛ける。
func afterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// addFlash は、bs のバッファの範囲 ranges に、d の間だけ色を付ける（決まった時間が過ぎたら消える色付けの口）。
// 範囲は、バージョン番号 version のテキストを指す。今のバージョン番号と違えば、付けても描かない。
// 一時的な色付けは本体の全体で 1 つだけなので、前の色付けがあれば（別のバッファのものでも）外してから付ける。
// d が 0 以下なら何もしない（前の色付けも外さない）。
// 時間の待ちはイベントループの外で行い、時間が来たらイベントループに戻して外す（基本設計 2.1）。
//
// req: FR-140
func (e *Editor) addFlash(bs *bufferState, version uint64, ranges []ui.Highlight, d time.Duration) {
	if d <= 0 {
		return
	}
	if e.flash != nil {
		e.flash.stop()
	}
	fl := &flash{bs: bs, buf: bs.buf, version: version, ranges: ranges}
	stop := e.stop
	fl.stop = e.afterFunc(d, func() {
		select {
		case e.inspect <- func() { e.removeFlash(fl) }:
		case <-stop:
		}
	})
	e.flash = fl
}

// removeFlash は、色付け fl を外す。既に外していれば（新しい色付けに置き換えた後に、止める前のタイマーが来た）何もしない。
//
// req: FR-140
func (e *Editor) removeFlash(fl *flash) {
	if e.flash == fl {
		e.flash = nil
	}
}

// flashHighlights は、一時的な色付けが bs のもので、付けたときから bs のバッファが変わっていなければ、その範囲を返す
// （ui.Frame.Highlights）。無ければ nil である。
//
// req: FR-140
func (e *Editor) flashHighlights(bs *bufferState) []ui.Highlight {
	fl := e.flash
	if fl == nil || fl.bs != bs || fl.buf != bs.buf || fl.version != bs.buf.Version() {
		return nil
	}
	return fl.ranges
}

// flashYank は、本体の y でヤンクした範囲 y に、設定 yank.highlightMs のミリ秒の間だけ色の名前 yank の色を付ける（FR-140）。
// 設定が 0 なら付けない。マクロの再生とドットリピートの中のヤンク（y.Replayed）には付けない（nvim 0.12.5 の vim.hl.on_yank の
// on_macro の既定と同じ）。設定は付けるたびに今の値を読む（基本設計 7.3 の「エディタ全体」の行の規則）。
//
// req: FR-140
func (e *Editor) flashYank(bs *bufferState, y *command.Yanked) {
	if y == nil || y.Replayed {
		return
	}
	ms := e.options.Get(option.YankHighlightMs).Int()
	e.addFlash(bs, y.Version, []ui.Highlight{{Range: y.Range, Style: ui.StyleYank}}, time.Duration(ms)*time.Millisecond)
}

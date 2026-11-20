package editor

import (
	"slices"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 決まった時間が過ぎたら消える色付け（基本設計 6.7 の「一時的な色付け」。FR-140）。
//
// 色付けは、バッファの範囲の一覧と色の名前の組で、バッファごとに持つ（bufferState.flashes）。
// 使う側（ヤンクした範囲の色の flashYank）は addFlash で付けるだけで、消すのはこのファイルの中の 2 つの規則による。
//
//   - 時間が過ぎたら消す。時間の待ちはイベントループの外のタイマー（Editor.afterFunc）で行い、時間が来たら inspect で
//     イベントループに戻して一覧から外す（基本設計 2.1。編集の状態はイベントループの goroutine だけが触る）。外した後は、
//     inspect の後の描き直し（Run の renderIdle）で画面から消える
//   - バッファが変わったら描かない。付けたときのバッファとバージョン番号を覚えておき、描くとき（flashHighlights）に今のものと
//     違えば一覧に入れない。バージョン番号は、本体の編集、拡張機能の編集、アンドゥ、リドゥ、読み直しのたびに進む（基本設計 4.2）ので、
//     変更の経路ごとに消す処理は無い。位置をずらして残すことはしない
//
// 同じ鍵（key）の色付けは、バッファごとに 1 つだけ持ち、新しく付けると前のものを外す（nvim の vim.hl.on_yank が、前のヤンクの色を
// 消してから付けるのと同じ）。

// flash は、決まった時間が過ぎたら消える色付けの 1 つである。
type flash struct {
	// key は、色付けの鍵である。同じバッファの同じ鍵の色付けは 1 つだけ持つ。
	key string
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
// bs に同じ key の色付けがあれば、外してから付ける。d が 0 以下なら何もしない。
// 時間の待ちはイベントループの外で行い、時間が来たらイベントループに戻して外す（基本設計 2.1）。
//
// req: FR-140
func (e *Editor) addFlash(bs *bufferState, key string, version uint64, ranges []ui.Highlight, d time.Duration) {
	if d <= 0 {
		return
	}
	bs.flashes = slices.DeleteFunc(bs.flashes, func(fl *flash) bool {
		if fl.key == key {
			fl.stop()
			return true
		}
		return false
	})
	fl := &flash{key: key, buf: bs.buf, version: version, ranges: ranges}
	stop := e.stop
	fl.stop = e.afterFunc(d, func() {
		select {
		case e.inspect <- func() { e.removeFlash(bs, fl) }:
		case <-stop:
		}
	})
	bs.flashes = append(bs.flashes, fl)
}

// removeFlash は、bs の色付け fl を外す。既に外していれば（同じ鍵の新しい色付けに置き換えた後に、止める前のタイマーが来た）何もしない。
//
// req: FR-140
func (e *Editor) removeFlash(bs *bufferState, fl *flash) {
	bs.flashes = slices.DeleteFunc(bs.flashes, func(f *flash) bool { return f == fl })
}

// flashHighlights は、bs の色付けのうち、付けたときから bs のバッファが変わっていないものの範囲を、付けた順に並べて返す
// （ui.Frame.Highlights。後に付けたものを上に重ねる）。無ければ nil である。
//
// req: FR-140
func (bs *bufferState) flashHighlights() []ui.Highlight {
	var out []ui.Highlight
	for _, fl := range bs.flashes {
		if fl.buf == bs.buf && fl.version == bs.buf.Version() {
			out = append(out, fl.ranges...)
		}
	}
	return out
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
	e.addFlash(bs, ui.StyleYank, y.Version, []ui.Highlight{{Range: y.Range, Style: ui.StyleYank}}, time.Duration(ms)*time.Millisecond)
}

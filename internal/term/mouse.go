package term

import (
	"strconv"
	"strings"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// マウスの報告を求める制御シーケンス（FR-180）。
// ボタンを押している間の動きも報告させ（DECSET 1002。Neovim 0.12.5 の 'mousemoveevent' がオフのときと同じ）、
// 報告の形を SGR（DECSET 1006）にする。SGR の形は、列と行が 223 を超えても壊れず、離したボタンも分かる。
// 止めるときは、両方を戻す。止める列は、端末を戻す列（seqLeave）にも含める。端末を子プロセスへ渡すとき（:!）、
// 一時停止のとき、終わるときに、マウスの報告を止めたまま渡すためである。
const (
	seqMouseOn  = "\x1b[?1002h\x1b[?1006h"
	seqMouseOff = "\x1b[?1006l\x1b[?1002l"
)

// SGR の形のマウスの報告のボタンの数（xterm の Ps の Cb）のビット。
const (
	sgrButtonMask = 3   // 下の 2 ビット。0 が左、1 が中、2 が右、3 はボタンを押していない動き
	sgrShift      = 4   // Shift
	sgrMeta       = 8   // Meta（Alt）
	sgrCtrl       = 16  // Ctrl
	sgrMotion     = 32  // ボタンを押したままの動き
	sgrWheel      = 64  // ホイール。下の 2 ビットが上、下、左、右
	sgrExtra      = 128 // 8 番より後のボタン（読まない）
)

// mouseKey は、SGR の形のマウスの報告（CSI < の後の引数 params。"ボタン;列;行"）を、マウスの出来事のキーと位置にする（FR-184）。
// release は、終わりの文字が m（ボタンを離した）かである。列と行は 1 から数えて届くので、0 から数える位置にする。
// 読めない報告（引数の数が違う、ボタンを押していない動き、8 番より後のボタン、ホイールを離した報告）は ok を false にする。
//
// req: FR-184
func mouseKey(params string, release bool) (Decoded, bool) {
	parts := strings.Split(params, ";")
	if len(parts) != 3 {
		return Decoded{}, false
	}
	var v [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Decoded{}, false
		}
		v[i] = n
	}
	b, x, y := v[0], v[1], v[2]
	if x < 1 || y < 1 || b&sgrExtra != 0 {
		return Decoded{}, false
	}
	var mod editcmd.Mod
	if b&sgrShift != 0 {
		mod |= editcmd.ModShift
	}
	if b&sgrMeta != 0 {
		mod |= editcmd.ModAlt
	}
	if b&sgrCtrl != 0 {
		mod |= editcmd.ModCtrl
	}
	button := b & sgrButtonMask
	var code editcmd.Code
	switch {
	case b&sgrWheel != 0:
		if release || b&sgrMotion != 0 {
			return Decoded{}, false
		}
		code = [...]editcmd.Code{
			editcmd.CodeScrollWheelUp, editcmd.CodeScrollWheelDown, editcmd.CodeScrollWheelLeft, editcmd.CodeScrollWheelRight,
		}[button]
	case button == sgrButtonMask:
		// ボタンを押していない動き（DECSET 1003 のときだけ届く）は読まない。
		return Decoded{}, false
	case release:
		code = [...]editcmd.Code{editcmd.CodeLeftRelease, editcmd.CodeMiddleRelease, editcmd.CodeRightRelease}[button]
	case b&sgrMotion != 0:
		code = [...]editcmd.Code{editcmd.CodeLeftDrag, editcmd.CodeMiddleDrag, editcmd.CodeRightDrag}[button]
	default:
		code = [...]editcmd.Code{editcmd.CodeLeftMouse, editcmd.CodeMiddleMouse, editcmd.CodeRightMouse}[button]
	}
	return Decoded{Key: editcmd.Key{Code: code, Mod: mod}, MouseRow: y - 1, MouseCol: x - 1}, true
}

// MultiClickTime は、続けて押したボタンを複数回のクリック（<2-LeftMouse> など）として数える間隔の上限である
// （Neovim 0.12.5 の 'mousetime' の既定の 500 ms。仮）。設定にはしていない。
const MultiClickTime = 500 * time.Millisecond

// clickCounter は、ボタンを押したことのキーに、何回目のクリックかを付ける（FR-183）。ゼロ値で使える。
//
// 数え方は Neovim 0.12.5 の check_multiclick と同じにした。同じボタンを、同じ修飾キーで、同じ位置で、前に押してから
// MultiClickTime より短い間に押したら、前の回数に 1 を足す。4 回の次は 1 回に戻る（Vim と同じ。5 回目は 1 回目として働く）。
// ほかの出来事（動き、離す、ホイール、キー）は数えるのを止めない。
// 時間を読む端末の側（client）で数えるのは、ESC の単独と ESC で始まる列の区別と同じく、届いた時間に依るためである。
type clickCounter struct {
	last     time.Time
	key      editcmd.Key
	row, col int
	n        int
}

// count は、d がボタンを押したことのキーなら、now に押したものとして回数を付けて返す。ほかの出来事はそのまま返す。
//
// req: FR-183
func (c *clickCounter) count(d Decoded, now time.Time) Decoded {
	if !d.Key.IsMousePress() {
		return d
	}
	if c.n > 0 && d.Key == c.key && d.MouseRow == c.row && d.MouseCol == c.col && now.Sub(c.last) < MultiClickTime {
		c.n = c.n%4 + 1
	} else {
		c.n = 1
	}
	c.last, c.key, c.row, c.col = now, d.Key, d.MouseRow, d.MouseCol
	d.Key = d.Key.WithClicks(c.n)
	return d
}

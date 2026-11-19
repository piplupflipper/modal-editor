package ex

import (
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// filterCommand は、:{range}!{cmd} の表の行である。名前の "!" は英字でないので commands の表には置かず、runOne が直接引く。
// 表の関数が Execute を経由して表を参照するのと同じく、初期化の循環を避けるために init で作る。
// :g の cmd の中では使えない（ShellRequest の doc）。
var filterCommand command

func init() {
	filterCommand = command{name: "!", min: 1, rng: rangeCurrent, modify: true, bar: barNone, run: runFilter}
}

// reportLines は、Vim の 'report' の既定値である。変えた行の数がこれを超えたら通知を出す。
const reportLines = 2

// ShellRequest は、:{range}!{cmd}、:r !{cmd}、:!{cmd} が呼び出し側へ求める、外部のコマンドの実行である（FR-80、FR-81）。
//
// Terminal が false（:{range}! と :r !）なら、呼び出し側は、Cmd をシェルで実行し、Input を標準入力へ渡し（nil なら何も渡さない）、
// 標準出力と標準エラー出力を書かれた順に合わせたものを Finish へ渡す
// （Vim の 'shellredir' の既定 ">%s 2>&1" と同じく、標準エラー出力もバッファへ入る）。
// Terminal が true（範囲の無い :!）なら、呼び出し側は端末を子プロセスへ渡して Cmd をシェルで実行し（Vim と同じ）、
// 終わったら出力を渡さずに Finish を呼ぶ。バッファは変わらない。
// 実行できなかった、中断したときは Finish を呼ばず、バッファは変えない。
type ShellRequest struct {
	// Cmd は、シェルへ渡すコマンドである。先頭の空白は除き、末尾の空白は残す（Vim と同じ）。空ではない。
	Cmd string
	// Input は、標準入力へ渡すテキストである。:{range}! では範囲の行を改行で終えて並べたもの、:r ! と :! では nil である。
	Input []byte
	// Terminal は、端末を子プロセスへ渡して実行すること（範囲の無い :!{cmd}。FR-80）を表す。
	Terminal bool

	read         bool
	line1, line2 int  // 0 始まり。:r では line2 がアドレスの行
	zero         bool // :0r
	version      uint64
}

// Finish は、外部のコマンドの出力 out と終了コード status で、バッファ b を変える。cursor は今のカーソルの行（0 始まり）である。
// 求めた後にバッファが変わっていれば（拡張機能の編集など）、範囲がずれているので何もせずに E319 を返す。
// 変更はアンドゥの 1 単位にする。status が 0 でなくても、Vim と同じく out でバッファを変え、警告の通知を出す。
//
// req: FR-81
func (r *ShellRequest) Finish(b *buffer.Buffer, st *State, cursor int, out []byte, status int) (Result, error) {
	if st == nil {
		st = &State{}
	}
	c := &execCtx{b: b, st: st, cursor: clampLine(b, cursor)}
	if r.Terminal {
		// :! はバッファを変えない。終了コードだけを知らせる（Vim の "shell returned N"）。
		statusNotice(c, status)
		return c.res(), nil
	}
	if b.Version() != r.version {
		return c.res(), errAt(0, codeNotAvailable, "外部のコマンドの実行中にバッファが変わったので、出力を捨てました")
	}
	b.BeginUndoGroup()
	defer b.EndUndoGroup()
	var err error
	if r.read {
		err = c.finishRead(r, outputLines(out))
	} else {
		err = c.finishFilter(r, outputLines(out))
	}
	if err == nil {
		statusNotice(c, status)
	}
	return c.res(), err
}

// runFilter は :{range}!{cmd} と :!{cmd} を解析し、Result.Shell で実行を求める（FR-80、FR-81）。
// 範囲の無い :!{cmd} は、端末を子プロセスへ渡す実行（ShellRequest.Terminal）である。編集を禁止したバッファでも使える。
// コマンドが空なら何もしない（Vim 9.1 の :! は E34 だが、:{range}! に合わせた）。
//
// req: FR-80, FR-81
func runFilter(c *execCtx, inv *invocation) error {
	cmd := shellArg(inv.arg)
	if cmd == "" {
		return nil
	}
	if inv.rangeCount == 0 {
		c.shell = &ShellRequest{Cmd: cmd, Terminal: true}
		return nil
	}
	var in strings.Builder
	if !c.b.NoLines() {
		for l := inv.line1; l <= inv.line2; l++ {
			in.WriteString(c.b.Line(l))
			in.WriteByte('\n')
		}
	}
	c.shell = &ShellRequest{Cmd: cmd, Input: []byte(in.String()), line1: inv.line1, line2: inv.line2, version: c.b.Version()}
	return nil
}

// finishFilter は、:{range}! の出力の行 lines で範囲を置き換える。
// 出力が空なら範囲の行を消し、バッファの全ての行を消したときは行の無いバッファにする（Vim 9.1 の :%!true と同じく空のファイルになる）。
// カーソルは、置き換えた範囲の最初の行（出力が空なら、消した範囲の次の行）の最初の空白でない文字へ移る。
// アンドゥの後のカーソルは、範囲の最初の行の先頭へ戻る。
// 範囲の行の数が 'report' の既定（2）を超えれば "N 行をフィルターしました" を通知する。
// どれも Vim 9.1 で確かめた動作である。ジャンプには当たらない（Vim 9.1 の do_filter は setpcmark を呼ばない）。
//
// req: FR-81
func (c *execCtx) finishFilter(r *ShellRequest, lines []string) error {
	l1, l2 := r.line1, r.line2
	all := l1 == 0 && l2 == c.b.LineCount()-1
	at := buffer.Position{Line: l1}
	var edit buffer.Edit
	if len(lines) == 0 {
		edit = deleteLinesEdit(c.b, l1, l2)
	} else {
		edit = buffer.Edit{
			Range: buffer.Range{Start: at, End: buffer.Position{Line: l2, Byte: len(c.b.Line(l2))}},
			Text:  strings.Join(lines, "\n"),
		}
	}
	if err := c.apply(edit); err != nil {
		return err
	}
	c.b.SetUndoCursor(at)
	if len(lines) == 0 && all {
		c.b.MarkNoLines()
	}
	c.setCursor(min(l1, c.b.LineCount()-1), CursorFirstNonBlank)
	if n := l2 - l1 + 1; n > reportLines {
		c.notice = Notice{Text: fmt.Sprintf("%d 行をフィルターしました", n)}
	}
	return nil
}

// runRead は :[N]r[ead] !{cmd} と :[N]r[ead]!{cmd} を解析し、Result.Shell で実行を求める（FR-81）。
// 標準入力には何も渡さない（Vim は端末を渡すが、FR-81 は端末を渡さない）。
// "!" の無い :r {file}（ファイルの読み込み）はまだ無いので、E319 にする。
//
// req: FR-81
func runRead(c *execCtx, inv *invocation) error {
	arg := inv.arg
	if !inv.bang {
		if !strings.HasPrefix(arg, "!") {
			return errAt(inv.argPos, codeNotAvailable, ":r {ファイル} はまだ使えません。:r !{コマンド} は使えます")
		}
		arg = arg[1:]
	}
	cmd := shellArg(arg)
	if cmd == "" {
		return nil
	}
	c.shell = &ShellRequest{Cmd: cmd, read: true, line2: inv.line2, zero: inv.zero, version: c.b.Version()}
	return nil
}

// finishRead は、:r ! の出力の行 lines を、アドレスの行の後（:0r は 1 行目の前）に入れる。
// 出力が空なら何もせず、カーソルも動かさない。
// カーソルは入れた最後の行の最初の空白でない文字へ移り、アンドゥの後のカーソルはアドレスの行（:0r は 1 行目）の先頭へ戻る。
// 入れた行の数が 'report' の既定（2）を超えれば "N 行増えました" を通知する。どれも Vim 9.1 で確かめた動作である。
// 行の無いバッファへ入れたときは、Vim 9.1 と同じく元の空の行を残す（:r は空の行の後に、:0r は前に入れる）。
//
// req: FR-81
func (c *execCtx) finishRead(r *ShellRequest, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	text := strings.Join(lines, "\n")
	var edit buffer.Edit
	var first int // 入れた最初の行（0 始まり）
	if r.zero {
		edit = buffer.Edit{Range: buffer.Range{}, Text: text + "\n"}
	} else {
		l := r.line2
		end := buffer.Position{Line: l, Byte: len(c.b.Line(l))}
		edit = buffer.Edit{Range: buffer.Range{Start: end, End: end}, Text: "\n" + text}
		first = l + 1
	}
	if err := c.apply(edit); err != nil {
		return err
	}
	c.b.SetUndoCursor(buffer.Position{Line: r.line2})
	c.setCursor(first+len(lines)-1, CursorFirstNonBlank)
	if len(lines) > reportLines {
		c.notice = Notice{Text: fmt.Sprintf("%d 行増えました", len(lines))}
	}
	return nil
}

// shellArg は、:{range}! と :r ! の cmd を返す。先頭の空白は除き、末尾の空白は残す（Vim と同じ）。
// 空なら、呼び出し側は何もしない（Vim 9.1 の do_filter も、空の cmd では誤りを出さずに戻る）。
func shellArg(arg string) string {
	return strings.TrimLeft(arg, " \t")
}

// statusNotice は、シェルの終了コードが 0 でなければ、それを警告の通知にする（Vim の "shell returned N" に当たる）。
func statusNotice(c *execCtx, status int) {
	if status != 0 {
		c.notice = Notice{Text: fmt.Sprintf("シェルが %d を返しました", status), Warning: true}
	}
}

// outputLines は、外部のコマンドの出力を行に分ける。最後の改行の後の空の行は数えない（"a\nb\n" と "a\nb" はどちらも 2 行）。
// 出力が空なら行は無い。改行だけの出力（"\n"）は空の行 1 つである。
func outputLines(out []byte) []string {
	if len(out) == 0 {
		return nil
	}
	s := strings.TrimSuffix(string(out), "\n")
	return strings.Split(s, "\n")
}

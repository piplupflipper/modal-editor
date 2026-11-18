package ex

import (
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// filterCommand は、:{range}!{cmd} の表の行である。名前の "!" は英字でないので commands の表には置かず、runOne が直接引く。
// 表の関数が Execute を経由して表を参照するのと同じく、初期化の循環を避けるために init で作る。
var filterCommand command

func init() {
	filterCommand = command{name: "!", min: 1, rng: rangeCurrent, inGlobal: true, bar: barNone, run: runFilter}
}

// reportLines は、Vim の 'report' の既定値である。変えた行の数がこれを超えたら通知を出す。
const reportLines = 2

// runFilter は :{range}!{cmd} を実行する（FR-81）。
// 範囲の行を、改行で終わるテキストとして cmd の標準入力へ渡し、cmd の出力の行で範囲を置き換える。
// 出力が空なら範囲の行を消し、バッファの全ての行を消したときは行の無いバッファにする（Vim 9.1 の :%!true と同じく空のファイルになる）。
// カーソルは、置き換えた範囲の最初の行（出力が空なら、消した範囲の次の行）の最初の空白でない文字へ移る。
// アンドゥの後のカーソルは、範囲の最初の行の先頭へ戻る。
// 範囲の行の数が 'report' の既定（2）を超えれば "N 行をフィルターしました" を通知する。
// どれも Vim 9.1 で確かめた動作である。ジャンプには当たらない（Vim 9.1 の do_filter は setpcmark を呼ばない）。
//
// 範囲の無い :!{cmd} は、端末を子プロセスへ渡す実行（FR-80）で、まだ無いので E319 にする。
//
// req: FR-81
func runFilter(c *execCtx, inv *invocation) error {
	if inv.rangeCount == 0 {
		return errAt(inv.namePos, codeNotAvailable, ":!{コマンド}（端末を渡す実行）はまだ使えません。範囲を付けた :{範囲}!{コマンド} は使えます")
	}
	cmd, err := shellArg(c, inv, inv.arg)
	if err != nil || cmd == "" {
		return err
	}
	l1, l2 := inv.line1, inv.line2
	var in strings.Builder
	if !c.b.NoLines() {
		for l := l1; l <= l2; l++ {
			in.WriteString(c.b.Line(l))
			in.WriteByte('\n')
		}
	}
	out, status, err := c.st.Shell(cmd, []byte(in.String()))
	if err != nil {
		return errAt(inv.argPos, "", "外部のコマンドを実行できません: %v", err)
	}
	lines := outputLines(out)
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
	// :g の印は、範囲の行を消してから出力の行を足したものとして動かす（範囲の中の印は消える。Vim と同じ）。
	c.replaced = append(c.replaced, lineReplace{start: l1, old: l2 - l1 + 1, new: 0}, lineReplace{start: l1, old: 0, new: len(lines)})
	c.setCursor(min(l1, c.b.LineCount()-1), CursorFirstNonBlank)
	c.notice = Notice{}
	if n := l2 - l1 + 1; n > reportLines {
		c.notice = Notice{Text: fmt.Sprintf("%d 行をフィルターしました", n)}
	}
	statusNotice(c, status)
	return nil
}

// runRead は :[N]r[ead] !{cmd} と :[N]r[ead]!{cmd} を実行する（FR-81）。
// cmd の出力の行を N 行目（省くとカーソルの行）の後に入れる。:0r は 1 行目の前に入れる。
// 標準入力には何も渡さない（Vim は端末を渡すが、FR-81 は端末を渡さない）。
// 出力が空なら何もせず、カーソルも動かさない。
// カーソルは入れた最後の行の最初の空白でない文字へ移り、アンドゥの後のカーソルは N 行目（:0r は 1 行目）の先頭へ戻る。
// 入れた行の数が 'report' の既定（2）を超えれば "N 行増えました" を通知する。どれも Vim 9.1 で確かめた動作である。
// 行の無いバッファへ入れたときは、Vim 9.1 と同じく元の空の行を残す（:r は空の行の後に、:0r は前に入れる）。
//
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
	cmd, err := shellArg(c, inv, arg)
	if err != nil || cmd == "" {
		return err
	}
	out, status, err := c.st.Shell(cmd, nil)
	if err != nil {
		return errAt(inv.argPos, "", "外部のコマンドを実行できません: %v", err)
	}
	lines := outputLines(out)
	c.notice = Notice{}
	statusNotice(c, status)
	if len(lines) == 0 {
		return nil
	}
	text := strings.Join(lines, "\n")
	var edit buffer.Edit
	var first int // 入れた最初の行（0 始まり）
	if inv.zero {
		edit = buffer.Edit{Range: buffer.Range{}, Text: text + "\n"}
	} else {
		l := inv.line2
		end := buffer.Position{Line: l, Byte: len(c.b.Line(l))}
		edit = buffer.Edit{Range: buffer.Range{Start: end, End: end}, Text: "\n" + text}
		first = l + 1
	}
	if err := c.apply(edit); err != nil {
		return err
	}
	c.b.SetUndoCursor(buffer.Position{Line: inv.line2})
	c.replaced = append(c.replaced, lineReplace{start: first, old: 0, new: len(lines)})
	c.setCursor(first+len(lines)-1, CursorFirstNonBlank)
	if len(lines) > reportLines && c.notice.Text == "" {
		c.notice = Notice{Text: fmt.Sprintf("%d 行増えました", len(lines))}
	}
	return nil
}

// shellArg は、:{range}! と :r ! の cmd を返す。先頭の空白は除き、末尾の空白は残す（Vim と同じ）。
// cmd が空なら空を返し、呼び出し側は何もしない（Vim 9.1 の do_filter も、空の cmd では誤りを出さずに戻る）。
// State.Shell が無ければ E319 にする。
func shellArg(c *execCtx, inv *invocation, arg string) (string, error) {
	cmd := strings.TrimLeft(arg, " \t")
	if cmd == "" {
		return "", nil
	}
	if c.st.Shell == nil {
		return "", errAt(inv.namePos, codeNotAvailable, "外部のコマンドは使えません")
	}
	return cmd, nil
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

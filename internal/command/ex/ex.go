// Package ex は、コマンドラインモードで確定した Ex コマンドの文字列を解析し、実行する（FR-08、FR-09）。
//
// 基本設計 2 章の internal/command の下にあり、internal/buffer だけを import する。
// バッファを編集するコマンド（:s、:g、:v、:d）は、バッファへ直接トランザクションを適用する。
// 保存、終了、ファイルを開くといったエディタの状態に関わるコマンド（:w、:q、:wq、:x、:e）は、
// Action の値として呼び出し側へ返し、このパッケージではファイルを読み書きしない。
//
// # 対応するコマンド
//
// コマンドは commands の表に登録する。対象の一覧（issue #9 の案）が裁定で増減しても、表の行を足し引きすればよい。
//
//	:w[rite][!] [file]   Action の ActionWrite を返す
//	:q[uit][!]           Action の ActionQuit を返す。! が無く、未保存の変更があれば E37 で失敗する
//	:wq[!] [file]        ActionWrite と ActionQuit を返す
//	:x[it][!] [file]     変更があるか file を指定したときだけ ActionWrite を返し、続けて ActionQuit を返す
//	:e[dit][!] [file]    Action の ActionEdit を返す。! が無く、未保存の変更があれば E37 で失敗する
//	:[range]s[ubstitute]/pat/rep/[flags] [count]
//	:[range]g[lobal][!]/pat/cmd
//	:[range]v[global]/pat/cmd
//	:[range]d[elete] [x] [count]
//	:[range]             範囲の最後の行へ移る（:3 など）
//
// # 範囲
//
// アドレスは、行番号、"."（現在の行）、"$"（最後の行）、"/pat/"（次の行から前方へ探す）、"?pat?"（前の行から後方へ探す）と、
// その後に続く "+n"、"-n" で書く。"%" は "1,$" と同じである。
// アドレスは "," か ";" で区切り、";" は次のアドレスを読む前に現在の行を前のアドレスへ移す。
// 検索は Vim の既定（wrapscan）と同じく、バッファの端で反対の端へ折り返す。
// マーク（"'<,'>" など）は未対応で、E319 を返す。
//
// # 正規表現
//
// パターンは Go の regexp（RE2）の文法で解釈する（FR-09）。Vim の正規表現の文法（\v、\< など）には変換しない。
// パターンの中の "\" と区切り文字の組は、区切り文字そのものとして扱う。
// 照合は行ごとに行い、パターンが行をまたいで一致することは無い。
// パターンを空にすると、同じ Execute の呼び出しの中で直前に使ったパターンを使う（:g/pat/s//rep/ など）。
//
// # 誤り
//
// 解析と実行の誤りは *Error で返す。Code は Vim のエラー番号、Pos はコマンドラインの中のバイト位置である。
package ex

import (
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/buffer"
)

// ActionKind は、呼び出し側が行うエディタの動作の種類を表す。
type ActionKind int

// 動作の種類。
const (
	// ActionWrite は、バッファの内容をファイルへ書き出す動作である。
	// 呼び出し側が buffer.WriteFile で書き、編集中のファイルへ書いたときは MarkSaved を呼ぶ。
	ActionWrite ActionKind = iota + 1
	// ActionQuit は、エディタ（またはウィンドウ）を閉じる動作である。
	ActionQuit
	// ActionEdit は、ファイルを開く動作である。File が空なら、編集中のファイルを読み直す。
	ActionEdit
)

// Action は、Ex コマンドが呼び出し側へ求めるエディタの動作である。
type Action struct {
	Kind ActionKind
	// File は、コマンドの引数に書かれたファイル名である。空なら編集中のファイルを指す。
	// 先頭と末尾の空白を除き、"\ " を空白に戻す以外の展開（%、~、ワイルドカード）はしない。
	File string
	// Force は、コマンドに "!" が付いていたかどうかである。
	// ActionWrite では既存のファイルや読み取り専用のファイルへの上書き、ActionQuit と ActionEdit では未保存の変更の破棄を表す。
	Force bool
}

// Deletion は、:d が消した行の記録である。
// レジスターは internal/command が持つので、呼び出し側がこの記録をレジスターへ入れる。
type Deletion struct {
	// Register は、:d の引数に書かれたレジスターの名前である。省略したときは 0 である。
	Register rune
	// Lines は、消した行を改行を含まずに並べたものである。行単位のテキストとして扱う。
	Lines []string
}

// Result は、Execute の結果である。
// Execute が誤りを返した場合も、誤りまでに行った変更と動作を保持する。
type Result struct {
	// Cursor は、実行した後のカーソルの行（0 始まり）である。
	// 列は返さない。Vim と同じく、行を移すコマンドの後は呼び出し側が行の最初の空白でない文字へ置く。
	Cursor int
	// Actions は、呼び出し側が順に行う動作である。
	Actions []Action
	// Changes は、バッファに適用した変更を適用した順に並べたものである。
	// 拡張機能へ差分を送る（FR-16）ときに使う。
	Changes []buffer.Change
	// Deletions は、:d が消した行を、消した順に並べたものである。
	Deletions []Deletion
}

// Execute は、コマンドラインの文字列 cmdline を Ex コマンドとして解析し、バッファ b に対して実行する。
// cursor は実行する前のカーソルの行（0 始まり）で、バッファの外を指していれば最も近い行に丸める。
// cmdline の先頭の ":" と空白は読み飛ばす。
//
// 1 回の Execute で行った編集は、アンドゥの 1 単位にまとめる（:g で多数の行を消しても u 1 回で戻る）。
// 誤りは *Error で返す。誤りを返した場合も、それまでに行った編集はバッファに残り、Result に記録される。
//
// req: FR-08
func Execute(b *buffer.Buffer, cursor int, cmdline string) (Result, error) {
	c := &execCtx{b: b, cursor: clampLine(b, cursor)}
	b.BeginUndoGroup()
	defer b.EndUndoGroup()
	err := c.run(cmdline, 0)
	return c.res(), err
}

// execCtx は、1 回の Execute の間の状態である。
type execCtx struct {
	b      *buffer.Buffer
	cursor int // 0 始まりの行
	// inGlobal は、:g の中で実行しているかどうかである。:g の入れ子を禁じるのに使う。
	inGlobal bool
	// lastPattern は、直前に使った正規表現のパターン（変換前の文字列）である。空のパターンはこれを使う。
	lastPattern string
	actions     []Action
	changes     []buffer.Change
	deletions   []Deletion
	// replaced は、行の置き換えの記録である。:g が、印を付けた行の番号を編集に合わせて動かすのに使う。
	replaced []lineReplace
}

// lineReplace は、start 行目（0 始まり）から old 行を、new 行で置き換えたことを表す。
type lineReplace struct {
	start, old, new int
}

func (c *execCtx) res() Result {
	return Result{Cursor: c.cursor, Actions: c.actions, Changes: c.changes, Deletions: c.deletions}
}

// apply は、edits を 1 つのトランザクションとして現在の版に適用し、変更を記録する。
func (c *execCtx) apply(edits ...buffer.Edit) error {
	ch, err := c.b.Apply(buffer.Transaction{BaseVersion: c.b.Version(), Edits: edits})
	if err != nil {
		return fmt.Errorf("ex: apply edits: %w", err)
	}
	c.changes = append(c.changes, ch)
	return nil
}

// run は、1 つの Ex コマンド src を解析して実行する。base は、src の先頭がコマンドラインの何バイト目かである（誤りの位置に使う）。
//
// req: FR-08
func (c *execCtx) run(src string, base int) error {
	s := &scanner{src: src, base: base}
	s.skipBlanksAndColons()
	r, err := c.parseRange(s)
	if err != nil {
		return err
	}
	s.skipBlanks()
	if s.eof() || s.peek() == '"' {
		// コマンド名が無ければ、範囲の最後の行へ移る（:3、:/pat/）。
		if r.count > 0 {
			c.cursor = max(r.line2, 1) - 1
		}
		return nil
	}

	namePos := s.pos()
	name := s.letters()
	if name == "" {
		return errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
	}
	cmd := lookup(name)
	if cmd == nil {
		return errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
	}
	if c.inGlobal && !cmd.inGlobal {
		return errAt(namePos, codeNotAvailable, ":global の中では使えません: %s", cmd.name)
	}

	inv := invocation{name: name, namePos: namePos}
	if s.peek() == '!' {
		if !cmd.bang {
			return errAt(s.pos(), codeNoBang, "! は使えません")
		}
		inv.bang = true
		s.next()
	}

	switch cmd.rng {
	case rangeNone:
		if r.count > 0 {
			return errAt(r.pos, codeNoRange, "範囲は使えません")
		}
	case rangeCurrent, rangeWhole:
		if r.count == 0 {
			if cmd.rng == rangeWhole {
				r.line1, r.line2 = 1, c.b.LineCount()
			} else {
				r.line1, r.line2 = c.cursor+1, c.cursor+1
			}
		}
		if r.line1 > r.line2 {
			r.line1, r.line2 = r.line2, r.line1
		}
		// 行番号 0 は、範囲を取るコマンドでは 1 行目として扱う（Vim と同じ）。
		r.line1, r.line2 = max(r.line1, 1)-1, max(r.line2, 1)-1
	}
	inv.line1, inv.line2, inv.rangeCount = r.line1, r.line2, r.count

	s.skipBlanks()
	inv.arg, inv.argPos = s.rest(), s.pos()
	return cmd.run(c, &inv)
}

// clampLine は、line（0 始まり）をバッファの中の行に丸める。
func clampLine(b *buffer.Buffer, line int) int {
	return min(max(line, 0), b.LineCount()-1)
}

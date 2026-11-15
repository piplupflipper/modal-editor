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
//	:[range]             範囲の最後の行へ移る（:3 など）。行数を超えた行番号は最後の行に丸める
//
// コマンドは "|" で区切って続けられる（:w|q、:e foo|q）。
// ファイル名や回数を取るコマンドでは、"\" の付かない最初の "|" で引数が終わり、"\|" は "|" そのものになる。
// :s は、パターンと置換の文字列の中の "|" を区切りとせず、フラグと回数の後の "|" で終わる。
// :g と :v は、Vim と同じく "|" を含めて行末までを cmd として受け取り、cmd の中の "|" は印の付いた行ごとに区切る。
//
// # 範囲
//
// アドレスは、行番号、"."（現在の行）、"$"（最後の行）、"/pat/"（次の行から前方へ探す）、"?pat?"（前の行から後方へ探す）と、
// その後に続く "+n"、"-n" で書く。"%" は "1,$" と同じである。
// 行数を超えた行番号は、コマンド名の無い範囲（:10）では最後の行に丸め、コマンドに付けた範囲（:10d、:1,10s/a/b/）と
// "|" が続く範囲（:10|）では E16 にする。負の行番号はどれでも E16 にする。Vim 9.1 の Ex モードでない動作に合わせている。
// アドレスは "," か ";" で区切り、";" は次のアドレスを読む前に現在の行を前のアドレスへ移す。
// 検索は Vim の既定（wrapscan）と同じく、バッファの端で反対の端へ折り返す。
// マーク（"'a"、"'<,'>" など）は State.Mark で行に解決する。
//
// # 正規表現
//
// パターンは Go の regexp（RE2）の文法で解釈する（FR-09）。Vim の正規表現の文法（\v、\< など）には変換しない。
// 利用者は Vim の書き方ではなく RE2 の書き方で書く。
// 例えば "\+" は Vim の「1 回以上」ではなくリテラルの "+"、"\(" はグループではなくリテラルの "("、"\|" は選択ではなくリテラルの "|" である。
// 1 回以上は "+"、グループは "(...)"、選択は "|"、単語の境界は "\b" と書く。
// パターンの中の "\" と区切り文字の組は、区切り文字そのものとして扱う。
// 照合は行ごとに行い、パターンが行をまたいで一致することは無い。
// パターンを空にすると、State.LastPattern（直前に使ったパターン）を使う（:g/pat/s//rep/ など）。
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
	// 先頭と末尾の空白を除き、"\ " を空白に、"\|" を "|" に戻す以外の展開（%、~、ワイルドカード）はしない。
	File string
	// Force は、コマンドに "!" が付いていたかどうかである。
	// ActionWrite では既存のファイルや読み取り専用のファイルへの上書き、ActionQuit と ActionEdit では未保存の変更の破棄を表す。
	Force bool
}

// Deletion は、:d が消した行の記録である。
// レジスターは internal/command が持つので、呼び出し側がこの記録をレジスターへ入れる。
type Deletion struct {
	// Register は、:d の引数に書かれたレジスターの名前である。省略したときは 0 である。
	// 書き込めるレジスターの名前（英字、"、-、_、*、+）であることは、このパッケージが検査する。
	Register rune
	// Lines は、消した行を改行を含まずに並べたものである。行単位のテキストとして扱う。
	Lines []string
}

// CursorMove は、実行した後にカーソルをどう置くかを表す。
type CursorMove int

// カーソルの置き方。列は buffer の行の内容から呼び出し側が決める。
const (
	// CursorUnchanged は、カーソルを動かさないことを表す（:w、:q、一致の無い :s/x/y/e など）。
	CursorUnchanged CursorMove = iota
	// CursorFirstNonBlank は、Result.Cursor の行の、最初の空白でない文字へ置くことを表す（:s、:d、:<行番号>）。
	CursorFirstNonBlank
	// CursorLineStart は、Result.Cursor の行の先頭（0 バイト目）へ置くことを表す。
	// :g が最後の印の行へ移り、cmd がカーソルを動かさなかったとき（:g/pat/s/x/y/ で x が無い）に当たる。
	CursorLineStart
)

// State は、Execute の呼び出しをまたいで保つ状態である。呼び出し側（internal/editor）が 1 つ持ち、毎回同じものを渡す。
// Execute は、この中身を Vim と同じ時機に更新する。
type State struct {
	// LastPattern は、直前に使った検索のパターン（Go の regexp の文法）である。
	// :s、:g、"/pat/" のアドレスで使ったものを記録し、空のパターン（:s//x/）はこれを使う。
	// ノーマルモードの / と n と共有する前提で、呼び出し側が書き換えてもよい。
	// コンパイルに失敗したパターンは記録しない。
	LastPattern string
	// LastSubstitute は、直前の :s のパターン、置換の文字列、フラグである。:s を一度も実行していなければ nil である。
	// 置換の文字列の ~ はこの Replacement に置き換わる。& と :&& は未実装だが、この値を使う想定である。
	LastSubstitute *Substitution
	// Mark は、マークの名前（'a の a、'< の <）を行（0 始まり）に解決する。
	// nil か、ok が false を返したときは、そのマークのアドレスを E20 にする。
	Mark func(name rune) (line int, ok bool)
}

// Substitution は、:s の 1 回分のパターン、置換の文字列、フラグである。
type Substitution struct {
	Pattern string
	// Replacement は、~ を直前の置換の文字列に展開した後の、Vim の書き方の置換の文字列である。
	Replacement string
	Global      bool // g フラグ
	IgnoreCase  bool // i フラグ（I で打ち消す）
}

// Result は、Execute の結果である。
// Execute が誤りを返した場合も、誤りまでに行った変更と動作を保持する。
type Result struct {
	// Cursor は、実行した後のカーソルの行（0 始まり）である。Move が CursorUnchanged なら、渡した行（丸めたもの）のままである。
	Cursor int
	// Move は、カーソルを動かしたかと、列の置き方である。Vim 9.1 の既定（'startofline' がオン）に合わせる。
	Move CursorMove
	// Actions は、呼び出し側が先頭から順に行う動作である。
	// 動作が失敗したら（:wq の ActionWrite の書き出しの失敗など）、残りの動作は行わずに捨て、誤りを利用者に示す。
	// 例えば :wq で書き出しに失敗したら終了しない。
	Actions []Action
	// Changes は、バッファに適用した変更を適用した順に並べたものである。
	// 拡張機能へ差分を送る（FR-16）ときに使う。
	Changes []buffer.Change
	// Deletions は、:d が消した行を、消した順に並べたものである。
	Deletions []Deletion
}

// Execute は、コマンドラインの文字列 cmdline を Ex コマンドとして解析し、バッファ b に対して実行する。
// cursor は実行する前のカーソルの行（0 始まり）で、バッファの外を指していれば最も近い行に丸める。
// cmdline の先頭の ":" と空白は読み飛ばし、"|" で区切った複数のコマンドを順に実行する（:w|q）。
// st は呼び出しをまたいで保つ状態で、Execute が更新する。nil なら、この呼び出しの中だけの状態を使う。
//
// 1 回の Execute で行った編集は、アンドゥの 1 単位にまとめる（:g で多数の行を消しても u 1 回で戻る）。
// 誤りは *Error で返す。誤りを返した場合も、それまでに行った編集はバッファに残り、Result に記録される。
// "|" で区切ったコマンドの途中で誤りがあれば、残りのコマンドは実行しない。
//
// req: FR-08
func Execute(b *buffer.Buffer, st *State, cursor int, cmdline string) (Result, error) {
	if st == nil {
		st = &State{}
	}
	c := &execCtx{b: b, st: st, cursor: clampLine(b, cursor)}
	b.BeginUndoGroup()
	defer b.EndUndoGroup()
	err := c.runLine(cmdline, 0)
	return c.res(), err
}

// execCtx は、1 回の Execute の間の状態である。
type execCtx struct {
	b      *buffer.Buffer
	st     *State
	cursor int // 0 始まりの行
	move   CursorMove
	// inGlobal は、:g の中で実行しているかどうかである。:g の入れ子を禁じるのに使う。
	inGlobal  bool
	actions   []Action
	changes   []buffer.Change
	deletions []Deletion
	// wroteVersion は、編集中のファイルへの ActionWrite を返した時点の版である。返していなければ nil である。
	wroteVersion *uint64
	// replaced は、行の置き換えの記録である。:g が、印を付けた行の番号を編集に合わせて動かすのに使う。
	replaced []lineReplace
}

// lineReplace は、start 行目（0 始まり）から old 行を、new 行で置き換えたことを表す。
type lineReplace struct {
	start, old, new int
}

func (c *execCtx) res() Result {
	return Result{Cursor: c.cursor, Move: c.move, Actions: c.actions, Changes: c.changes, Deletions: c.deletions}
}

// setCursor は、カーソルを line 行目（0 始まり）へ移し、列の置き方を記録する。
func (c *execCtx) setCursor(line int, mv CursorMove) {
	c.cursor = line
	c.move = mv
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

// runLine は、"|" で区切った Ex コマンドの列 src を順に実行する。base は、src の先頭がコマンドラインの何バイト目かである（誤りの位置に使う）。
//
// req: FR-08
func (c *execCtx) runLine(src string, base int) error {
	for {
		next, nextPos, more, err := c.runOne(src, base)
		if err != nil || !more {
			return err
		}
		src, base = next, nextPos
	}
}

// runOne は、src の先頭の Ex コマンドを 1 つ解析して実行する。
// "|" の後に続くコマンドがあれば、その文字列と位置を next、nextPos に入れ、more を true にして返す。
//
// req: FR-08
func (c *execCtx) runOne(src string, base int) (next string, nextPos int, more bool, err error) {
	s := &scanner{src: src, base: base}
	s.skipBlanksAndColons()
	r, err := c.parseRange(s)
	if err != nil {
		return "", 0, false, err
	}
	s.skipBlanks()
	if s.eof() || s.peek() == '"' || s.peek() == '|' {
		// コマンド名が無ければ、範囲の最後の行へ移る（:3、:/pat/）。
		// 後に "|" が続くとき（:3|）は、Vim では :print に当たるので、範囲がバッファの外なら E16 にする。
		// 続かないとき（:3、:1,10）は、行数を超えた line2 を最後の行に丸め、line1 は見ない（Vim 9.1 の ex_range_without_command と同じ）。
		bar := s.peek() == '|'
		if r.count > 0 {
			if bar {
				if err := r.check(c.b.LineCount()); err != nil {
					return "", 0, false, err
				}
			}
			c.setCursor(min(max(r.line2, 1), c.b.LineCount())-1, CursorFirstNonBlank)
		}
		if bar {
			s.next()
			return s.rest(), s.pos(), true, nil
		}
		return "", 0, false, nil
	}

	namePos := s.pos()
	name := s.letters()
	if name == "" {
		return "", 0, false, errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
	}
	cmd := lookup(name)
	if cmd == nil {
		return "", 0, false, errAt(namePos, codeNotEditorCommand, "エディタのコマンドではありません: %s", strings.TrimSpace(src))
	}
	if c.inGlobal && !cmd.inGlobal {
		return "", 0, false, errAt(namePos, codeNotAvailable, ":global の中では使えません: %s", cmd.name)
	}

	inv := invocation{name: name, namePos: namePos}
	if s.peek() == '!' {
		if !cmd.bang {
			return "", 0, false, errAt(s.pos(), codeNoBang, "! は使えません")
		}
		inv.bang = true
		s.next()
	}

	switch cmd.rng {
	case rangeNone:
		if r.count > 0 {
			return "", 0, false, errAt(r.pos, codeNoRange, "範囲は使えません")
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
			r.pos1, r.pos2 = r.pos2, r.pos1
		}
		// 範囲を伴うコマンドは、行数を超えた行を丸めずに E16 にする（:4d、:1,4s/a/b/。Vim 9.1 と同じ）。
		if err := r.check(c.b.LineCount()); err != nil {
			return "", 0, false, err
		}
		// 行番号 0 は、範囲を取るコマンドでは 1 行目として扱う（Vim と同じ）。
		r.line1, r.line2 = max(r.line1, 1)-1, max(r.line2, 1)-1
	}
	inv.line1, inv.line2, inv.rangeCount = r.line1, r.line2, r.count

	s.skipBlanks()
	inv.arg, inv.argPos = s.rest(), s.pos()
	if cmd.bar == barSplit {
		var n int
		inv.arg, n, more = splitBar(inv.arg)
		if more {
			inv.next, inv.nextPos, inv.hasNext = s.rest()[n:], s.pos()+n, true
		}
	}
	if err := cmd.run(c, &inv); err != nil {
		return "", 0, false, err
	}
	return inv.next, inv.nextPos, inv.hasNext, nil
}

// splitBar は、引数 arg を、"\" の付かない最初の "|" の手前までと、その後に分ける。
// 手前の部分の "\|" は "|" に戻す（ファイル名に "|" を書くとき）。
// n は、"|" を含めて読んだバイト数である。"|" が無ければ more は false である。
func splitBar(arg string) (part string, n int, more bool) {
	var sb strings.Builder
	for i := 0; i < len(arg); i++ {
		switch {
		case arg[i] == '\\' && i+1 < len(arg) && arg[i+1] == '|':
			sb.WriteByte('|')
			i++
		case arg[i] == '|':
			return sb.String(), i + 1, true
		default:
			sb.WriteByte(arg[i])
		}
	}
	return sb.String(), len(arg), false
}

// clampLine は、line（0 始まり）をバッファの中の行に丸める。
func clampLine(b *buffer.Buffer, line int) int {
	return min(max(line, 0), b.LineCount()-1)
}

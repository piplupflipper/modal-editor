package command

import (
	"strings"

	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// CommandLine は、コマンドラインモードで入力中の文字列（先頭の ":" を除く）を返す。
func (s *State) CommandLine() string { return string(s.cmdline) }

// cmdlineKey は、コマンドラインモードのキーを 1 個処理する。
//
// 文字の追加、<BS>（空なら抜ける）、<CR>（実行）、<Esc>（取り消し）だけを扱う最小の行の編集である。
// カーソルの移動や履歴といった行の編集は、表示と一緒に呼び出し側（internal/editor）が持つ想定で、
// その場合は確定した文字列を ExecuteCommandLine へ渡す。
// ここで最小の編集を持つのは、マクロの再生（@a）で記録した :s/a/b/<CR> のようなキーを、State の中だけで再生するためである。
//
// 検索（/ と ?）のパターンの入力も、同じ行の編集を使う。<CR> で検索を実行し、<Esc> と空の行の <BS> で、
// 入力を始めたモード（ノーマルモードかビジュアルモード）へ戻る（startSearch）。
//
// req: FR-01
func (s *State) cmdlineKey(k editcmd.Key) {
	switch {
	case k.Mod != 0:
	case k.Code == editcmd.CodeEsc:
		if s.search != nil {
			s.cancelSearch()
			return
		}
		s.mode = ModeNormal
		s.cmdline = s.cmdline[:0]
	case k.Code == editcmd.CodeEnter:
		line := string(s.cmdline)
		s.cmdline = s.cmdline[:0]
		if s.search != nil {
			s.finishSearch(line)
			return
		}
		s.mode = ModeNormal
		s.runCommandLine(line)
	case k.Code == editcmd.CodeBackspace:
		if len(s.cmdline) == 0 {
			if s.search != nil {
				s.cancelSearch()
				return
			}
			s.mode = ModeNormal
			return
		}
		s.cmdline = s.cmdline[:len(s.cmdline)-1]
	case k.Code == editcmd.CodeTab:
		s.cmdline = append(s.cmdline, '\t')
	case k.Code == editcmd.CodeRune:
		s.cmdline = append(s.cmdline, k.Rune)
	}
}

// ExecuteCommandLine は、コマンドラインで確定した文字列 line を Ex コマンドとして実行し、ノーマルモードへ戻る（FR-08）。
// 行の編集を呼び出し側が持つ場合に、<CR> で確定した文字列を渡す口である。
// :w や :q のようなエディタの動作は Result.Actions で返す。:d が消した行はレジスターに入れる。
//
// req: FR-01
func (s *State) ExecuteCommandLine(line string) Result {
	s.res = Result{}
	if s.search != nil {
		// 検索のパターンの入力の途中に Ex コマンドの実行を求められたら、検索を取り消してから実行する。
		s.cancelSearch()
	}
	if s.mode.visual() {
		s.exitVisual()
	}
	s.mode = ModeNormal
	s.cmdline = s.cmdline[:0]
	s.runCommandLine(line)
	return s.take()
}

// runCommandLine は、Ex コマンドを実行し、結果をカーソルとレジスターと Result に反映する。
func (s *State) runCommandLine(line string) {
	if strings.TrimLeft(line, ": \t") == "" {
		return
	}
	// コマンドラインの編集と、後に置いた拡張機能のコマンドの編集（:s/a/X/|dup.line）を、アンドゥの 1 単位にまとめる
	// （Vim 9.1 で :s/a/X/|Dup y の後の u 1 回で両方が戻るのと同じ）。
	// 期限 0 で保留した拡張機能のコマンドの結果は、届いたときに別の単位で適用する。
	s.buf.BeginUndoGroup()
	defer s.buf.EndUndoGroup()
	res, err := ex.Execute(s.buf, &s.Ex, s.cursor.Line, line)
	s.applyExResult(res, err)
}

// applyExResult は、Ex コマンドの結果を、カーソルとレジスターと Result に反映する。
// 確認が要る結果（Result.Confirm）は、Result.Confirm へ渡す。
func (s *State) applyExResult(res ex.Result, err error) {
	if res.Confirm != nil {
		s.res.Confirm = res.Confirm
	}
	if res.Jumped {
		// 実行する前のカーソルの位置を記録する（Vim の setpcmark は編集の前に働く）。
		// Ex コマンドの変更を Result に入れる前に記録するので、呼び出し側は記録した位置をその変更でずらす（:d が消した行の中の位置など）。
		s.markJump()
	}
	s.res.Changes = append(s.res.Changes, res.Changes...)
	var extCmds []ex.Action
	for _, a := range res.Actions {
		if a.Kind == ex.ActionExtCommand {
			extCmds = append(extCmds, a)
			continue
		}
		s.res.Actions = append(s.res.Actions, a)
	}
	// 1 回の Ex コマンドの行の中の削除（:g/pat/d、:d|d）は、履歴の 1 件として扱う（基本設計 4.5）。
	// 最初に積んだ後の削除は、積む代わりに履歴の先頭を置き換える（Vim の無名のレジスターが最後に消した行を持つのと同じ）。
	pushed := false
	for _, d := range res.Deletions {
		if s.writeRegister(d.Register, Register{Text: strings.Join(d.Lines, "\n"), Linewise: true}, true, pushed) {
			pushed = true
		}
	}
	switch res.Move {
	case ex.CursorFirstNonBlank:
		s.setCursorNormal(s.firstNonBlankPos(min(res.Cursor, s.lastLine())))
	case ex.CursorLineStart:
		s.setCursorNormal(pos(min(res.Cursor, s.lastLine()), 0))
	case ex.CursorKeepColumn:
		s.setCursorNormal(pos(min(res.Cursor, s.lastLine()), s.cursor.Byte))
	case ex.CursorUnchanged:
		s.setCursorNormal(s.cursor)
	}
	if err != nil {
		s.fail(err)
	} else if s.playDepth > 0 && len(s.res.Actions) > 0 && s.Navigator != nil {
		// マクロの再生の途中なら、動作（溜めた前の行のものを含む）をその場で行う（#172）。
		// 呼び出し側へ返して Feed の後に行うと、:e や :bn の後のキーが元のバッファで動く。
		// 別のバッファへ移ったら ErrBufferSwitched で止まり、再生の残りは移った先のバッファで続く（playKeys）。
		// 誤りで止まった行（:w|foo）の動作は、再生も止まるので、これまでどおり呼び出し側が Feed の後に行う。
		acts := s.res.Actions
		s.res.Actions = nil
		if err := s.Navigator.RunActions(acts); err != nil {
			s.fail(err)
		}
	}
	// 拡張機能のコマンド（ex.ActionExtCommand）は、呼び出し側へ返さずにここで問い合わせる。
	// キーから呼んだ拡張機能の操作と同じく、期限の扱い（FR-17、FR-18）と再生中の待ち（FR-19）を通すためである。
	// 引数が行末までを取るので、コマンドラインの最後のコマンドである。
	// :w などの動作（Result.Actions）の後には置けない（ex が E319 にする）ので、動作との順序は問題にならない。
	for _, a := range extCmds {
		s.runExtCommand(a)
	}
}

// Confirm は、Result.Confirm で求められた確認への利用者の答えを受け取り、yes なら Ex コマンドの続きを実行する。
// no なら何もせずに空の Result を返す。
// 確認を求めた Feed などの後、バッファと State を変えずに呼ぶ（ex.Confirm.Resume の前提）。
// 続きがまた確認を求めたら、Result.Confirm に入る。
//
// req: FR-08
func (s *State) Confirm(cf *ex.Confirm, yes bool) Result {
	s.res = Result{}
	if cf == nil || !yes {
		return s.take()
	}
	res, err := cf.Resume(s.buf, &s.Ex)
	s.applyExResult(res, err)
	return s.take()
}

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
// req: FR-01
func (s *State) cmdlineKey(k editcmd.Key) {
	switch {
	case k.Mod != 0:
	case k.Code == editcmd.CodeEsc:
		s.mode = ModeNormal
		s.cmdline = s.cmdline[:0]
	case k.Code == editcmd.CodeEnter:
		line := string(s.cmdline)
		s.mode = ModeNormal
		s.cmdline = s.cmdline[:0]
		s.runCommandLine(line)
	case k.Code == editcmd.CodeBackspace:
		if len(s.cmdline) == 0 {
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
	s.lastCmd = line
	s.registers.cmdline = line
	res, err := ex.Execute(s.buf, &s.Ex, s.cursor.Line, line)
	s.applyExResult(res, err)
}

// applyExResult は、Ex コマンドの結果を、カーソルとレジスターと Result に反映する。
// 確認が要る結果（Result.Confirm）は、Result.Confirm へ渡す。
func (s *State) applyExResult(res ex.Result, err error) {
	if res.Confirm != nil {
		s.res.Confirm = res.Confirm
	}
	s.res.Changes = append(s.res.Changes, res.Changes...)
	s.res.Actions = append(s.res.Actions, res.Actions...)
	for _, d := range res.Deletions {
		s.registers.delete(d.Register, Register{Text: strings.Join(d.Lines, "\n"), Linewise: true}, false, false)
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

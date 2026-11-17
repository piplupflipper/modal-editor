package editor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// regListState は、:registers で出したレジスターの一覧の窓である（FR-54。基本設計 4.5）。
// 一覧は出した時点の項目の写しで、窓を出している間のキーは一覧の操作にする（regListKey）。
type regListState struct {
	// bs は、一覧を出したバッファである。貼り付けはこのバッファの State に渡す。
	bs *bufferState
	// entries は、一覧の項目（command.Registers.List）である。
	entries []command.RegisterEntry
	// pushes は、一覧を作ったときの command.Registers.Pushes である。貼るときに、履歴が変わっていないかを確かめる。
	pushes uint64
	// sel は、選んでいる項目の添字である。
	sel int
}

// regListTitle は、一覧の窓の見出しである。
const regListTitle = "レジスター  j/k で選び <CR> で貼る  <Esc> で閉じる"

// errNoRegisters は、一覧に出す項目が無いときの :registers の失敗である。
var errNoRegisters = errors.New("レジスターも履歴も空です")

// openRegisterList は、バッファ bs のレジスターと履歴の一覧の窓を出す（:registers）。項目が無ければ errNoRegisters を返す。
//
// req: FR-54
func (e *Editor) openRegisterList(bs *bufferState) error {
	regs := bs.state.Registers()
	entries := regs.List()
	if len(entries) == 0 {
		return errNoRegisters
	}
	e.regList = &regListState{bs: bs, entries: entries, pushes: regs.Pushes()}
	return nil
}

// regListKey は、一覧の窓を出している間のキーを処理する。
// j と <Down> で次の項目へ、k と <Up> で前の項目へ移り（端では止まる）、<CR> で選んだ項目をカーソルの後に貼って窓を閉じ、
// <Esc> で貼らずに閉じる。他のキーは、Vim の confirm() の問いと同じく捨てる。
//
// req: FR-54
func (e *Editor) regListKey(k editcmd.Key) {
	l := e.regList
	switch k {
	case editcmd.Rune('j'), editcmd.Special(editcmd.CodeDown):
		l.sel = min(l.sel+1, len(l.entries)-1)
	case editcmd.Rune('k'), editcmd.Special(editcmd.CodeUp):
		l.sel = max(l.sel-1, 0)
	case editcmd.Special(editcmd.CodeEnter):
		e.regList = nil
		if l.bs != e.cur {
			return
		}
		_ = e.handleResult(l.bs, l.bs.state.PutFromList(l.entries[l.sel], l.pushes)) // 失敗は handleResult が知らせる
	case editcmd.Esc:
		e.regList = nil
	}
}

// uiRegList は、一覧の窓を描画の入力にする。出したバッファからカーソルが移っていれば、窓を閉じて nil を返す。
func (e *Editor) uiRegList() *ui.List {
	l := e.regList
	if l == nil {
		return nil
	}
	if l.bs != e.cur {
		e.regList = nil
		return nil
	}
	items := make([]string, len(l.entries))
	for i, en := range l.entries {
		items[i] = regListLabel(en)
	}
	return &ui.List{Title: regListTitle, Items: items, Selected: l.sel}
}

// regListLabel は、一覧の 1 項目の表示を返す。
// 履歴の項目は新しい順の番号（1 から）を、名前付きのレジスターは "a の形の名前を先頭に置き、続けて行単位（l）か文字単位（c）かと、
// テキストの 1 行目を置く。2 行目以降があれば、その行数を末尾に書く。
func regListLabel(en command.RegisterEntry) string {
	name := fmt.Sprintf("%2d", en.Index+1)
	if en.Name != 0 {
		name = fmt.Sprintf("\"%c", en.Name)
	}
	kind := 'c'
	if en.Register.Linewise {
		kind = 'l'
	}
	first, rest, multi := strings.Cut(en.Register.Text, "\n")
	label := fmt.Sprintf("%s  %c  %s", name, kind, first)
	if multi {
		label += fmt.Sprintf("（他 %d 行）", strings.Count(rest, "\n")+1)
	}
	return label
}

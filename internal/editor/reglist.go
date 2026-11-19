package editor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/ui"
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
	// replayed は、マクロの再生の途中で出した一覧であることを表す。
	// 再生の中の動作はその場で行う（#172）ので、出した後も再生の残りのキーがレジスターを変え、バッファを移ることがある。
	// そのため、再生が終わった後の最初のキーか描画で、カーソルのあるバッファのレジスターから項目を作り直す（refreshRegList）。
	replayed bool
}

// regListTitle は、一覧の窓の見出しである。
const regListTitle = "レジスター  j/k で選び <CR> で貼る  <Esc> で閉じる"

// errNoRegisters は、一覧に出す項目が無いときの :registers の失敗である。
var errNoRegisters = errors.New("レジスターも履歴も空です")

// errRegListNoRoom は、画面が小さく一覧の窓を描けないときの :registers の失敗である。
var errRegListNoRoom = errors.New("画面が小さいので、レジスターの一覧を出せません")

// regListFits は、一覧の窓を描ける大きさの画面か（ui の drawList が描くか）を返す。
// 見出しと 1 項目の 2 行が表示領域に収まり、窓の左右の余白と 1 文字が幅に収まるときである。
// 描けない窓を開くと、見えないままキーを一覧の操作に取られるので、開かずに知らせる（PR #171 のレビュー）。
func (e *Editor) regListFits() bool {
	return e.renderer.TextRows() >= 2 && e.renderer.Width >= 3
}

// openRegisterList は、バッファ bs のレジスターと履歴の一覧の窓を出す（:registers）。項目が無ければ errNoRegisters を返す。
//
// req: FR-54
func (e *Editor) openRegisterList(bs *bufferState) error {
	regs := bs.state.Registers()
	entries := regs.List()
	if len(entries) == 0 {
		return errNoRegisters
	}
	if !e.regListFits() {
		return errRegListNoRoom
	}
	e.regList = &regListState{bs: bs, entries: entries, pushes: regs.Pushes(), replayed: bs.state.Replaying()}
	return nil
}

// refreshRegList は、マクロの再生の途中で出した一覧（regListState.replayed）の項目を、カーソルのあるバッファのレジスターから作り直す。
// 再生はキー 1 個の処理の中で終わるので、一覧の操作のキー（regListKey）と描画（uiRegList）の時点では、再生は終わっている。
// 一覧は、再生の終わったバッファに付け直す（再生の残りが :e などで移った場合も、そのバッファで貼る）。
func (e *Editor) refreshRegList() {
	l := e.regList
	if l == nil || !l.replayed {
		return
	}
	regs := e.cur.state.Registers()
	l.bs, l.entries, l.pushes, l.sel, l.replayed = e.cur, regs.List(), regs.Pushes(), 0, false
	if len(l.entries) == 0 {
		e.regList = nil
	}
}

// regListKey は、一覧の窓を出している間のキーを処理する。
// j と <Down> で次の項目へ、k と <Up> で前の項目へ移り（端では止まる）、<CR> で選んだ項目をカーソルの後に貼って窓を閉じ、
// <Esc> で貼らずに閉じる。他のキーは、Vim の confirm() の問いと同じく捨てる。
//
// req: FR-54
func (e *Editor) regListKey(k editcmd.Key) {
	e.refreshRegList()
	l := e.regList
	if l == nil {
		return
	}
	if !e.regListFits() {
		// 開いた後に画面が小さくなり、窓が見えなくなった。見えない一覧の操作にしないよう、このキーは捨てて閉じ、知らせる。
		e.regList = nil
		e.showError(errRegListNoRoom)
		return
	}
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
	e.refreshRegList()
	l := e.regList
	if l == nil {
		return nil
	}
	if l.bs != e.cur {
		e.regList = nil
		return nil
	}
	// 名前の列と種類の列は、全ての項目で同じ幅にそろえ、テキストの 1 行目の始まりをそろえる（履歴が 100 件でも、行数が 2 桁でもずれない）。
	nameW, kindW := 2, 1
	for _, en := range l.entries {
		nameW = max(nameW, len(regListName(en)))
		kindW = max(kindW, len(regListKind(en.Register)))
	}
	items := make([]string, len(l.entries))
	for i, en := range l.entries {
		first, _, _ := strings.Cut(en.Register.Text, "\n")
		items[i] = fmt.Sprintf("%*s  %-*s  %s", nameW, regListName(en), kindW, regListKind(en.Register), first)
	}
	return &ui.List{Title: regListTitle, Items: items, Selected: l.sel}
}

// regListName は、一覧の 1 項目の名前の列を返す。履歴の項目は新しい順の番号（1 から）、名前付きのレジスターは "a の形である。
func regListName(en command.RegisterEntry) string {
	if en.Name != 0 {
		return fmt.Sprintf("\"%c", en.Name)
	}
	return fmt.Sprint(en.Index + 1)
}

// regListKind は、一覧の 1 項目の種類の列を返す。行単位なら l、文字単位なら c で、2 行以上にわたるテキストなら "l+2" のように
// 2 行目以降の行数を続ける。テキストの 1 行目より左に置くので、狭い窓で右の端を切っても行数は残る（PR #171 のレビュー）。
func regListKind(r command.Register) string {
	kind := "c"
	if r.Linewise {
		kind = "l"
	}
	if n := strings.Count(r.Text, "\n"); n > 0 {
		kind += fmt.Sprintf("+%d", n)
	}
	return kind
}

package editor

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/rpc"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// request は、拡張機能からのリクエストのイベントが共通に持つメソッドである。
type request interface {
	Done() <-chan struct{}
	ReplyError(err error) error
}

// closed は、リクエストが取り消されたか、拡張機能の接続が閉じたかを返す。
func closed(r request) bool {
	select {
	case <-r.Done():
		return true
	default:
		return false
	}
}

// invalidParams は、params の誤りを表す JSON-RPC のエラーを返す。
func invalidParams(format string, args ...any) error {
	return &rpc.Error{Code: rpc.CodeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

// handleEvent は、exthost のイベントを 1 件処理する（基本設計 5.3）。
// リクエストは、取り消されていれば応答せずに捨てる（exthost.Event の doc）。
func (e *Editor) handleEvent(ev exthost.Event) {
	if r, ok := ev.(request); ok && closed(r) {
		return
	}
	switch ev := ev.(type) {
	case *exthost.QueryResult:
		if p := e.deferred; p != nil && ev.Tag == p.tag {
			c := p.client
			if c == nil || c.detached {
				c = e.op
			}
			e.withView(c, func() { e.applyDeferred(p, ev) })
		}
		// それ以外は、期限を超えたか取り消した問い合わせの結果なので捨てる（FR-17）。
	case *exthost.StateChange:
		e.stateChange(ev)
	case *exthost.EditRequest:
		e.editRequest(ev)
	case *exthost.MessageShow:
		e.withView(e.extView(ev.Extension), func() {
			e.message = ui.Message{Text: ev.Params.Text, Level: messageLevel(ev.Params.Level)}
		})
	case *exthost.DecorationSet:
		e.setDecorations(ev.Extension, ev.Params)
	case *exthost.DiagnosticSet:
		e.setDiagnostics(ev.Extension, ev.Params)
	case *exthost.SignSet:
		e.setSigns(ev.Extension, ev.Params)
	case *exthost.PopupShow:
		e.showPopup(ev.Params)
	case *exthost.OutputAppend:
		e.appendOutput(ev.Extension, ev.Params)
	case *exthost.EditorStateRequest:
		_ = ev.Reply(e.editorState())
	case *exthost.EditorOpenRequest:
		e.withView(e.extView(ev.Extension), func() { e.editorOpenRequest(ev) })
	case *exthost.BufferSaveRequest:
		e.bufferSaveRequest(ev)
	case *exthost.DiagnosticListRequest:
		_ = ev.Reply(e.diagnosticList(ev.Params))
	case *exthost.ChoiceAskRequest:
		if e.asker == nil && !e.asking() && e.activeChoice() == nil {
			// 問いは、その拡張機能へ問い合わせを始めた client に出す（基本設計 1.2）。
			e.asker = e.extView(ev.Extension)
		}
		e.choices = append(e.choices, ev)
	default:
		e.log.Warn("未知のイベント", "type", fmt.Sprintf("%T", ev))
	}
}

// extView は、拡張機能 extID が後から届けるもの（editor/open、message/show、choice/ask）を出す client を返す。
// その拡張機能へ最後に問い合わせを始めた client で、その client が終わっていれば（または問い合わせていなければ）操作中の client である
// （PR #223 のレビューの裁定要 2 へのオーケストレータの決定。仮）。
func (e *Editor) extView(extID string) *clientView {
	if c := e.extClient[extID]; c != nil && !c.detached {
		return c
	}
	return e.op
}

func messageLevel(l ext.MessageLevel) ui.MessageLevel {
	switch l {
	case ext.MessageWarning:
		return ui.MessageWarning
	case ext.MessageError:
		return ui.MessageError
	case ext.MessageInfo:
	}
	return ui.MessageInfo
}

// stateChange は、拡張機能の状態の変化を処理する。異常で終わったら 1 行のメッセージで知らせ、表示のデータを消す（基本設計 8 章、6.7）。
func (e *Editor) stateChange(ev *exthost.StateChange) {
	switch ev.State {
	case exthost.StateRestartWait, exthost.StateDisabled:
		e.clearExtension(ev.Extension)
		if ev.State == exthost.StateDisabled && ev.Err == nil && e.extStopped[ev.Extension] {
			// :ext sync が削除の後に止めた（exthost.Host.Disable）。
			// 異常ではないので知らせず、:ext sync の結果のメッセージを残す。
			delete(e.extStopped, ev.Extension)
			break
		}
		text := fmt.Sprintf("拡張機能 %s が異常終了しました", ev.Extension)
		if ev.State == exthost.StateDisabled {
			text = fmt.Sprintf("拡張機能 %s は失敗が続いたので無効にしました", ev.Extension)
		}
		if ev.Err != nil {
			text += ": " + ev.Err.Error()
		}
		e.message = ui.Message{Text: text, Level: ui.MessageError}
	case exthost.StateNotStarted:
		// 起動した後に未起動へ戻るのは、:ext sync が入れ替えの後に止めたとき（exthost.Host.Reload）だけである。
		e.clearExtension(ev.Extension)
	case exthost.StateStarting, exthost.StateRunning, exthost.StateStopping:
	}
}

// editRequest は、buffer/edit を処理する。元にしたバージョン番号が現在と一致した場合だけ適用する（FR-15）。
//
// req: FR-15
func (e *Editor) editRequest(ev *exthost.EditRequest) {
	bs := e.findBuffer(ev.Params.Buffer)
	if bs == nil {
		_ = ev.ReplyError(invalidParams("unknown buffer %d", ev.Params.Buffer))
		return
	}
	if bs.buf.ReadOnly() {
		// 編集を禁止したバッファは、編集の無い（noLines と endOfLine だけの）要求も断る（FR-118。readOnlyError の doc）。
		_ = ev.ReplyError(readOnlyError())
		return
	}
	ch, err := e.applyExternal(bs, fromExtTx(ev.Params.Transaction))
	switch {
	case errors.Is(err, buffer.ErrVersionMismatch):
		_ = ev.Reply(ext.BufferEditResult{Applied: false, Version: ext.Version(bs.buf.Version())})
	case err != nil:
		_ = ev.ReplyError(invalidParams("%v", err))
	default:
		e.applyLineState(bs, ev.Params)
		_ = ev.Reply(ext.BufferEditResult{Applied: ch.Before != ch.After, Version: ext.Version(bs.buf.Version())})
	}
}

// applyLineState は、buffer/edit の noLines と endOfLine を、適用した後のバッファへ反映する（ext.BufferEditParams）。
// 外から書き換えたファイルを :e で読み直したのと同じ状態（reload）にするためのもので、変更としては扱わない。
// 拡張機能へ送る finalNewline が変われば、buffer/open で送り直させる。
func (e *Editor) applyLineState(bs *bufferState, p ext.BufferEditParams) {
	if p.NoLines == nil && p.EndOfLine == nil {
		return
	}
	if p.EndOfLine != nil {
		bs.buf.SetTrailingNewline(*p.EndOfLine)
	}
	switch {
	case p.NoLines == nil:
	case *p.NoLines:
		bs.buf.MarkNoLines()
	default:
		bs.buf.MarkHasLines()
	}
	e.syncFinalNewline(bs)
}

// applyExternal は、利用者の操作でない編集（buffer/edit、buffer/willSave）を、アンドゥの 1 単位として適用する。
// カーソルは、診断と同じ規則で編集に合わせてずらす。
// アンドゥの単位には、適用する前のカーソルを記録する（Vim で setline() の変更を u で取り消したときと同じ位置へ戻る）。
func (e *Editor) applyExternal(bs *bufferState, tx buffer.Transaction) (buffer.Change, error) {
	before := bs.state.Cursor()
	ch, err := bs.buf.Apply(tx)
	if err != nil {
		return ch, err
	}
	if len(tx.Edits) > 0 {
		bs.buf.SetUndoCursor(before)
	}
	e.commitChange(bs, ch)
	bs.state.SetCursor(shiftAll(bs.state.Cursor(), ch.Edits))
	return ch, nil
}

// popupMark は、拡張機能の操作を問い合わせた State と、その時点で State が解釈していたキーの数である。
type popupMark struct {
	state *command.State
	keys  uint64
}

// showPopup は、popup/show を、カーソルのあるバッファで、バージョン番号が現在と一致し、
// カーソルが表示の基準の位置（Position）にあり、最後の拡張機能の操作の問い合わせの後に次のキーを解釈していない場合だけ表示する。
// 本文は、ui がカーソルの近くに浮かべて描く（ui.Frame.Popup）。
//
// 表示は次のキー入力かカーソルの移動で消す（基本設計 6.7）ので、届いた時点でそのどちらかが起きていれば、既に消えたものとして出さない。
// マクロの再生の途中のホバー（FR-19。#210）は、再生を終えてからこの関数に届く（待つ間のイベントは hold で取っておく）。
// 記録したときは次のキー（j、zz など）で消えていたので、再生の後に残さないためである。
// 再生のキーも command.State.KeysFed が数えるので、移動しないキー（zz）や動いて戻るキー（jk）が続いても出さない（#217 のレビュー）。
// 段階 3 では、バッファをカーソルのあるタブに持つ client の画面に出す（応答が届くまでに、別の client がキーを打っていてもよい。#215）。
//
// req: FR-30, FR-19
func (e *Editor) showPopup(p ext.PopupShowParams) {
	bs := e.findBuffer(p.Buffer)
	if bs == nil || bs.owner == nil || uint64(p.Version) != bs.buf.Version() || bs.state.Cursor() != fromExtPos(p.Position) {
		return
	}
	e.withView(bs.owner, func() {
		if e.cur != bs {
			return
		}
		if m := e.popupMark; m.state == bs.state && m.keys != bs.state.KeysFed() {
			return
		}
		e.popup = &popupState{bs: bs, cursor: bs.state.Cursor(), text: p.Text, markdown: p.Format == ext.PopupMarkdown}
	})
}

// modeName は、command のモードを ext のモードの名前にする。
func modeName(m command.Mode) ext.Mode {
	switch m {
	case command.ModeInsert:
		return ext.ModeInsert
	case command.ModeVisual:
		return ext.ModeVisual
	case command.ModeVisualLine:
		return ext.ModeVisualLine
	case command.ModeCommandLine:
		return ext.ModeCommandLine
	case command.ModeNormal:
	}
	return ext.ModeNormal
}

// editorState は、editor/state の結果を作る（基本設計 6.7）。
func (e *Editor) editorState() ext.EditorStateResult {
	st := e.cur.state
	res := ext.EditorStateResult{
		Buffer: e.cur.id,
		Cursor: toExtPos(st.Cursor()),
		Mode:   modeName(st.Mode()),
	}
	if start, end, linewise, ok := st.Selection(); ok {
		if end.Less(start) {
			start, end = end, start
		}
		b := e.cur.buf
		if linewise {
			start.Byte = 0
			end.Byte = len(b.Line(end.Line))
		} else {
			// 終わりが行末の後（空の行を含む）なら、その行の改行まで含める。d が消す範囲と揃える（#89）。
			line := b.Line(end.Line)
			switch {
			case end.Byte < len(line):
				_, n := utf8.DecodeRuneInString(line[end.Byte:])
				end.Byte += n
			case end.Line+1 < b.LineCount():
				end = buffer.Position{Line: end.Line + 1}
			}
		}
		res.Selections = []ext.Range{toExtRange(buffer.Range{Start: start, End: end})}
	}
	for _, bs := range e.bufs {
		res.Buffers = append(res.Buffers, ext.BufferInfo{
			Buffer: bs.id, Path: bs.path, FileType: bs.fileType,
			Version: ext.Version(bs.buf.Version()), Modified: bs.buf.Modified(),
		})
	}
	return res
}

// editorOpenRequest は、editor/open を処理する。
func (e *Editor) editorOpenRequest(ev *exthost.EditorOpenRequest) {
	bs, moved, err := e.editorOpen(ev.Params)
	if err != nil {
		_ = ev.ReplyError(err)
		return
	}
	_ = ev.Reply(ext.EditorOpenResult{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Moved: moved})
}

// editorOpen は、editor/open の params に従い、ファイルかバッファを開き、カーソルを動かす（基本設計 6.7）。
// 既に開いているファイルなら、新しく開かずにそのバッファを使う。
// 開いたバッファは exthost.Host.OpenBuffer で稼働中の拡張機能すべてへ送るので、要求した拡張機能にも応答より先に届く。
func (e *Editor) editorOpen(p ext.EditorOpenParams) (*bufferState, bool, error) {
	if err := p.Validate(); err != nil {
		return nil, false, invalidParams("%v", err)
	}
	var bs *bufferState
	prev, reused := e.cur, false
	if p.Buffer != nil {
		bs = e.findBuffer(*p.Buffer)
		if bs == nil {
			return nil, false, invalidParams("unknown buffer %d", *p.Buffer)
		}
	} else {
		// 起動したときの名前の無い空のバッファから移るなら、:e と同じくその番号を使い直す（openFile の focus）。
		// 入力の途中のバッファからは移さないので、使い直さない。
		mode := e.cur.state.Mode()
		reuse := p.Focus && reusable(e.cur) && mode != command.ModeInsert && mode != command.ModeCommandLine
		var err error
		bs, err = e.openFile(p.Path, reuse)
		if err != nil {
			return nil, false, err
		}
		reused = reuse && bs == prev
	}
	// カーソルのあるバッファの中の移動は、ジャンプとして記録する（定義へ移動の後の <C-o> で戻れるように）。
	// 別のバッファへ移るときと、使い直したときは、openFile か enter が移る前の位置を記録するので、移った先のバッファの中の移動は記録しない。
	jump := prev == bs && !reused
	if p.Focus && e.cur != bs {
		if e.cur.state.Mode() == command.ModeInsert || e.cur.state.Mode() == command.ModeCommandLine {
			// 入力の途中のバッファからは移さない。開くことと、そのバッファのカーソルを動かすことだけを行う。
			e.message = infoMessage(fmt.Sprintf("%s を開きました", displayName(bs)))
		} else {
			e.enter(bs)
		}
	}
	return bs, e.moveCursor(bs, p, jump), nil
}

// bufferSaveRequest は、buffer/save を処理する。利用者の保存と同じ保存の流れを通る（基本設計 6.7）。
func (e *Editor) bufferSaveRequest(ev *exthost.BufferSaveRequest) {
	bs := e.findBuffer(ev.Params.Buffer)
	if bs == nil {
		_ = ev.ReplyError(invalidParams("unknown buffer %d", ev.Params.Buffer))
		return
	}
	if v := ev.Params.Version; v != nil && uint64(*v) != bs.buf.Version() {
		_ = ev.Reply(ext.BufferSaveResult{Saved: false, Version: ext.Version(bs.buf.Version())})
		return
	}
	if bs.path == "" {
		_ = ev.ReplyError(invalidParams("buffer %d has no file name", bs.id))
		return
	}
	// 外で変わったファイルへは書き出さない。拡張機能の応答を待たせたまま利用者に問うと、問いの間に取っておくイベントが溜まるので、
	// 問わずに断り、利用者が :w か :w! で決める（基本設計 6.7）。
	err := writeConflict(bs)
	if errors.Is(err, errChangedOnDisk) {
		err = fmt.Errorf("警告: 読み込んだ後にファイル \"%s\" が変更されているので、拡張機能の保存を断りました (:w! で上書きします)", e.shortName(bs))
	}
	if err == nil {
		err = e.save(bs, bs.path, ev.Params.SkipHooks)
	}
	if err != nil {
		e.showError(err)
	}
	_ = ev.Reply(ext.BufferSaveResult{Saved: err == nil, Version: ext.Version(bs.buf.Version())})
}

// diagnosticList は、diagnostic/list の結果を作る。位置は編集に合わせてずらした、現在のバージョン番号の位置である。
func (e *Editor) diagnosticList(p ext.DiagnosticListParams) ext.DiagnosticListResult {
	res := ext.DiagnosticListResult{Entries: []ext.DiagnosticEntry{}}
	for _, bs := range e.bufs {
		if p.Buffer != nil && *p.Buffer != bs.id {
			continue
		}
		for _, id := range slices.Sorted(maps.Keys(bs.diagnostics)) {
			res.Entries = append(res.Entries, ext.DiagnosticEntry{
				Buffer: bs.id, Extension: id, Version: ext.Version(bs.buf.Version()), Diagnostics: bs.diagnostics[id],
			})
		}
	}
	return res
}

// activeChoice は、表示中の choice/ask を返す。取り下げられた問い（Done が閉じた）は、待たせている問いからも捨て、
// 答えた問いと同じく choiceDone に数える。
func (e *Editor) activeChoice() *exthost.ChoiceAskRequest {
	n := len(e.choices)
	e.choices = slices.DeleteFunc(e.choices, func(c *exthost.ChoiceAskRequest) bool { return closed(c) })
	e.choiceDone += n - len(e.choices)
	if len(e.choices) == 0 {
		return nil
	}
	return e.choices[0]
}

// endChoices は、一続きの問いを終え、何件目かの数と知らせを消す。問いを出していない間に、利用者のキーか貼り付けを
// 処理するときに呼ぶ（handleKey、handlePaste）。
//
// 待たせている問いが無くなった時点では終えない。opencode 1.19.4 は、並べて呼んだツールの権限の問いを、
// 前の問いの答えを受け取ってから次を送る（2026-11-16 に手元で確かめた）ので、次の問いが届くまでの間に待ちの列が空になる。
// 利用者が問いの他に何もしていなければ、届いた問いを同じ一続きの次の問いとして数え、前の問いへの答えを添える。
func (e *Editor) endChoices() {
	e.choiceDone, e.choiceNote, e.choiceDropped = 0, "", 0
}

// choiceKey は、表示中の問いへのキーを処理する。1〜9 で選び、<Esc> で取り消す。
// 答えを受け付けたことはメッセージの行に 1 行で出し、次の問いを出すときはその上の知らせの行にも出す（#151）。
// 他のキーは、Vim 9.1 の confirm() の問いと同じく答えにせずに捨てるが、Vim と違って捨てた数を問いの上に出す（dropChoiceInput）。
func (e *Editor) choiceKey(k editcmd.Key) {
	c := e.choices[0]
	which := "問い"
	if e.choiceDone > 0 || len(e.choices) > 1 {
		which = fmt.Sprintf("問い %d ", e.choiceDone+1)
	}
	var ack string
	switch {
	case k == editcmd.Esc:
		_ = c.Reply(ext.ChoiceAskResult{Cancelled: true})
		ack = which + "を取り消しました"
	case k.Plain() && k.Rune >= '1' && k.Rune <= '9' && int(k.Rune-'1') < len(c.Params.Choices):
		ch := c.Params.Choices[k.Rune-'1']
		_ = c.Reply(ext.ChoiceAskResult{Selected: ch.ID})
		ack = fmt.Sprintf("%sに「%s」と答えました", which, strings.Join(strings.Fields(ch.Label), " "))
	default:
		e.dropChoiceInput()
		return
	}
	e.choices = e.choices[1:]
	e.choiceDone++
	e.choiceDropped = 0
	e.choiceNote = ack
	e.message = infoMessage(ack)
}

// dropChoiceInput は、問いの答えにならないキーか貼り付けを捨てたことを数える。数は問いの上の知らせの行に出す（#151）。
// コマンドラインの入力（:output<CR>）を問いの間に打つと、何も起きずに捨てられたように見えるためである。
func (e *Editor) dropChoiceInput() {
	e.choiceDropped++
}

// maxChoiceKeys は、choice/ask の選択肢を選べる番号のキーの数（1〜9）である。
const maxChoiceKeys = 9

// uiChoice は、表示中の問いを描画の入力にする。番号のキーで選べる最初の 9 つを選択肢として出し、残りは数だけを出す（#144）。
// 一続きの問いの 2 件目以降か、後に待たせている問いがあれば何件目かを出し、前の問いへの答え、待たせている問いの数、
// 捨てた入力の数を知らせの行に添える（#151）。
func (e *Editor) uiChoice() *ui.Choice {
	c := e.choices[0]
	chs := c.Params.Choices
	n := min(len(chs), maxChoiceKeys)
	labels := make([]string, n)
	for i, ch := range chs[:n] {
		labels[i] = ch.Label
	}
	uc := &ui.Choice{Title: c.Params.Title, Labels: labels, Omitted: len(chs) - n}
	if e.choiceDone > 0 || len(e.choices) > 1 {
		uc.Index = e.choiceDone + 1
	}
	var notes []string
	if e.choiceNote != "" {
		notes = append(notes, e.choiceNote)
	}
	if w := len(e.choices) - 1; w > 0 {
		notes = append(notes, fmt.Sprintf("この後に %d 件の問いが待っています", w))
	}
	if e.choiceDropped > 0 {
		notes = append(notes, fmt.Sprintf("番号と <Esc> 以外の入力は捨てます（捨てた数: %d）", e.choiceDropped))
	}
	uc.Note = strings.Join(notes, "\n")
	return uc
}

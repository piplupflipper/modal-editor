package editor

import (
	"errors"
	"fmt"
	"maps"
	"slices"
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
			e.applyDeferred(p, ev)
		}
		// それ以外は、期限を超えたか取り消した問い合わせの結果なので捨てる（FR-17）。
	case *exthost.StateChange:
		e.stateChange(ev)
	case *exthost.EditRequest:
		e.editRequest(ev)
	case *exthost.MessageShow:
		e.message = ui.Message{Text: ev.Params.Text, Level: messageLevel(ev.Params.Level)}
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
		e.editorOpenRequest(ev)
	case *exthost.BufferSaveRequest:
		e.bufferSaveRequest(ev)
	case *exthost.DiagnosticListRequest:
		_ = ev.Reply(e.diagnosticList(ev.Params))
	case *exthost.ChoiceAskRequest:
		e.choices = append(e.choices, ev)
	default:
		e.log.Warn("未知のイベント", "type", fmt.Sprintf("%T", ev))
	}
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
		text := fmt.Sprintf("拡張機能 %s が異常終了しました", ev.Extension)
		if ev.State == exthost.StateDisabled {
			text = fmt.Sprintf("拡張機能 %s は失敗が続いたので無効にしました", ev.Extension)
		}
		if ev.Err != nil {
			text += ": " + ev.Err.Error()
		}
		e.message = ui.Message{Text: text, Level: ui.MessageError}
	case exthost.StateNotStarted, exthost.StateStarting, exthost.StateRunning, exthost.StateStopping:
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

// showPopup は、popup/show を、カーソルのあるバッファで、バージョン番号が現在と一致する場合だけ表示する。
// 本文は、ui がカーソルの近くに浮かべて描く（ui.Frame.Popup）。
//
// req: FR-30
func (e *Editor) showPopup(p ext.PopupShowParams) {
	bs := e.cur
	if bs.id != p.Buffer || uint64(p.Version) != bs.buf.Version() {
		return
	}
	e.popup = &popupState{bs: bs, cursor: bs.state.Cursor(), text: p.Text}
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
	if p.Buffer != nil {
		bs = e.findBuffer(*p.Buffer)
		if bs == nil {
			return nil, false, invalidParams("unknown buffer %d", *p.Buffer)
		}
	} else {
		var err error
		bs, err = e.openFile(p.Path, false)
		if err != nil {
			return nil, false, err
		}
	}
	if p.Focus && e.cur != bs {
		if e.cur.state.Mode() == command.ModeInsert || e.cur.state.Mode() == command.ModeCommandLine {
			// 入力の途中のバッファからは移さない。開くことと、そのバッファのカーソルを動かすことだけを行う。
			e.message = infoMessage(fmt.Sprintf("%s を開きました", displayName(bs)))
		} else {
			e.switchTo(bs)
		}
	}
	return bs, e.moveCursor(bs, p), nil
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

// activeChoice は、表示中の choice/ask を返す。取り下げられた問い（Done が閉じた）は捨てる。
func (e *Editor) activeChoice() *exthost.ChoiceAskRequest {
	for len(e.choices) > 0 && closed(e.choices[0]) {
		e.choices = e.choices[1:]
	}
	if len(e.choices) == 0 {
		return nil
	}
	return e.choices[0]
}

// choiceKey は、表示中の問いへのキーを処理する。1〜9 で選び、<Esc> で取り消す。他のキーは捨てる。
func (e *Editor) choiceKey(k editcmd.Key) {
	c := e.choices[0]
	switch {
	case k == editcmd.Esc:
		_ = c.Reply(ext.ChoiceAskResult{Cancelled: true})
	case k.Plain() && k.Rune >= '1' && k.Rune <= '9' && int(k.Rune-'1') < len(c.Params.Choices):
		_ = c.Reply(ext.ChoiceAskResult{Selected: c.Params.Choices[k.Rune-'1'].ID})
	default:
		return
	}
	e.choices = e.choices[1:]
}

// maxChoiceKeys は、choice/ask の選択肢を選べる番号のキーの数（1〜9）である。
const maxChoiceKeys = 9

// uiChoice は、問いを描画の入力にする。番号のキーで選べる最初の 9 つを選択肢として出し、残りは数だけを出す（#144）。
func uiChoice(c *exthost.ChoiceAskRequest) *ui.Choice {
	chs := c.Params.Choices
	n := min(len(chs), maxChoiceKeys)
	labels := make([]string, n)
	for i, ch := range chs[:n] {
		labels[i] = ch.Label
	}
	return &ui.Choice{Title: c.Params.Title, Labels: labels, Omitted: len(chs) - n}
}

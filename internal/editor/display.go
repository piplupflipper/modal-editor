package editor

import (
	"sort"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// decorationSet は、1 つの拡張機能が 1 つのバッファに付けた構文の色の一覧である（decoration/set）。
type decorationSet struct {
	version     ext.Version
	decorations []ext.Decoration
}

// outputKey は、出力の欄を見分ける、拡張機能と欄の名前の組である（output/append）。
type outputKey struct {
	ext, channel string
}

// popupState は、表示中の popup/show である。カーソルを動かすと消す。
type popupState struct {
	bs     *bufferState
	cursor buffer.Position
	text   string
}

var noMessage = ui.Message{}

func infoMessage(text string) ui.Message { return ui.Message{Text: text, Level: ui.MessageInfo} }

// syncChanges は、changes のうち、まだ exthost へ知らせていないものを知らせる（FR-16）。
func (e *Editor) syncChanges(bs *bufferState, changes []buffer.Change) {
	for _, ch := range changes {
		e.commitChange(bs, ch)
	}
}

// commitChange は、バッファに適用した変更 ch を、exthost へ差分で送り（FR-16）、診断と印をずらし、履歴に覚える。
// 既に知らせた変更（ch.After がバージョン番号の記録 sent 以下）は何もしない。
// マクロの再生の途中で問い合わせる前に送った変更が、Feed の結果にもう一度入っているためである。
//
// req: FR-16
func (e *Editor) commitChange(bs *bufferState, ch buffer.Change) {
	if ch.After <= bs.sent || ch.Before == ch.After {
		return
	}
	if err := e.host.ChangeBuffer(bs.id, ch); err != nil {
		e.log.Error("buffer/change を送れない", "buffer", bs.id, "err", err)
	}
	bs.sent = ch.After
	for id, ds := range bs.diagnostics {
		bs.diagnostics[id] = shiftDiagnostics(ds, ch.Edits)
	}
	for id, ss := range bs.signs {
		bs.signs[id] = shiftSigns(ss, ch.Edits)
	}
	bs.history = append(bs.history, ch)
	if n := len(bs.history); n > maxHistory {
		bs.history = append([]buffer.Change(nil), bs.history[n-maxHistory:]...)
	}
}

// editsSince は、バージョン番号 v から現在までの編集を、適用した順に返す。
// v が覚えている変更の範囲より古いか、現在より新しければ ok が false である。
func (bs *bufferState) editsSince(v uint64) (eds []buffer.Edit, ok bool) {
	cur := bs.buf.Version()
	switch {
	case v == cur:
		return nil, true
	case v > cur:
		return nil, false
	}
	for i, ch := range bs.history {
		if ch.Before == v {
			for _, c := range bs.history[i:] {
				eds = append(eds, c.Edits...)
			}
			return eds, true
		}
	}
	return nil, false
}

// shiftPos は、位置 p を、編集 ed の後のテキストの位置へずらす（基本設計 6.7 の表示のデータ）。
//
// 編集の範囲より後ろの位置は、編集で増減した行数だけ動かし、編集の終わりと同じ行にあれば列のバイト数も動かす。
// 範囲の終わりと同じ位置は「後ろ」に数える。範囲と重なる位置（範囲の開始と同じで、終わりとは違う位置を含む）は、
// 編集の後のテキストの範囲の開始に寄せる。範囲より前の位置は動かさない。
func shiftPos(p buffer.Position, ed buffer.Edit) buffer.Position {
	s, end := ed.Range.Start, ed.Range.End
	switch {
	case p.Less(s):
		return p
	case p.Less(end):
		return s
	}
	nl := strings.Count(ed.Text, "\n")
	newEnd := buffer.Position{Line: s.Line + nl}
	if nl == 0 {
		newEnd.Byte = s.Byte + len(ed.Text)
	} else {
		newEnd.Byte = len(ed.Text) - strings.LastIndex(ed.Text, "\n") - 1
	}
	if p.Line == end.Line {
		return buffer.Position{Line: newEnd.Line, Byte: newEnd.Byte + p.Byte - end.Byte}
	}
	return buffer.Position{Line: p.Line + newEnd.Line - end.Line, Byte: p.Byte}
}

// shiftAll は、位置 p を編集の列 eds で順にずらす。
func shiftAll(p buffer.Position, eds []buffer.Edit) buffer.Position {
	for _, ed := range eds {
		p = shiftPos(p, ed)
	}
	return p
}

func shiftExtRange(r ext.Range, eds []buffer.Edit) ext.Range {
	return ext.Range{Start: toExtPos(shiftAll(fromExtPos(r.Start), eds)), End: toExtPos(shiftAll(fromExtPos(r.End), eds))}
}

// shiftDiagnostics は、診断の範囲を編集の列でずらした写しを返す。
func shiftDiagnostics(ds []ext.Diagnostic, eds []buffer.Edit) []ext.Diagnostic {
	out := make([]ext.Diagnostic, len(ds))
	for i, d := range ds {
		d.Range = shiftExtRange(d.Range, eds)
		out[i] = d
	}
	return out
}

// shiftSigns は、行の印を編集の列でずらした写しを返す。印は行の先頭の位置として扱う。
func shiftSigns(ss []ext.Sign, eds []buffer.Edit) []ext.Sign {
	out := make([]ext.Sign, len(ss))
	for i, s := range ss {
		s.Line = shiftAll(buffer.Position{Line: s.Line}, eds).Line
		out[i] = s
	}
	return out
}

// setDiagnostics は、diagnostic/set の一覧を、バージョン番号に応じてずらすか捨てて持つ（基本設計 6.7 の表）。
// 空の一覧はその組の一覧を消す。持ったかを返す。
func (e *Editor) setDiagnostics(extID string, p ext.DiagnosticSetParams) bool {
	bs := e.findBuffer(p.Buffer)
	if bs == nil {
		return false
	}
	eds, ok := bs.editsSince(uint64(p.Version))
	if !ok {
		return false
	}
	if len(p.Diagnostics) == 0 {
		delete(bs.diagnostics, extID)
		return true
	}
	bs.diagnostics[extID] = shiftDiagnostics(p.Diagnostics, eds)
	return true
}

// setSigns は、sign/set の一覧を、diagnostic/set と同じ規則で持つ。
func (e *Editor) setSigns(extID string, p ext.SignSetParams) bool {
	bs := e.findBuffer(p.Buffer)
	if bs == nil {
		return false
	}
	eds, ok := bs.editsSince(uint64(p.Version))
	if !ok {
		return false
	}
	if len(p.Signs) == 0 {
		delete(bs.signs, extID)
		return true
	}
	bs.signs[extID] = shiftSigns(p.Signs, eds)
	return true
}

// setDecorations は、decoration/set の一覧を持つ。現在より新しいバージョン番号の一覧は捨てる。
// 基本設計 6.1 のとおり、古い一覧も置き換えずに持ち、新しい一覧が届くまで前の色のまま描く（位置はずらさない）。
func (e *Editor) setDecorations(extID string, p ext.DecorationSetParams) {
	bs := e.findBuffer(p.Buffer)
	if bs == nil || uint64(p.Version) > bs.buf.Version() {
		return
	}
	if len(p.Decorations) == 0 {
		delete(bs.decorations, extID)
	} else {
		bs.decorations[extID] = decorationSet{version: p.Version, decorations: p.Decorations}
	}
	bs.rebuildDecorations()
}

// rebuildDecorations は、拡張機能ごとの一覧を合わせて、描画に渡す ui.Decorations を作り直す。
func (bs *bufferState) rebuildDecorations() {
	ids := make([]string, 0, len(bs.decorations))
	for id := range bs.decorations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var hs []ui.Highlight
	for _, id := range ids {
		for _, d := range bs.decorations[id].decorations {
			hs = append(hs, ui.Highlight{Range: fromExtRange(d.Range), Style: d.Style})
		}
	}
	if len(hs) == 0 {
		bs.decoView = nil
		return
	}
	bs.decoView = ui.NewDecorations(hs)
}

// clearExtension は、異常で終わった拡張機能の表示のデータを、すべてのバッファから消す（基本設計 6.7）。
func (e *Editor) clearExtension(extID string) {
	for _, bs := range e.bufs {
		delete(bs.diagnostics, extID)
		delete(bs.signs, extID)
		if _, ok := bs.decorations[extID]; ok {
			delete(bs.decorations, extID)
			bs.rebuildDecorations()
		}
	}
}

// appendOutput は、出力の欄へテキストを書き足す。行数が上限を超えたら古い行から捨てる。
func (e *Editor) appendOutput(extID string, p ext.OutputAppendParams) {
	key := outputKey{ext: extID, channel: p.Channel}
	lines := e.outputs[key]
	parts := strings.Split(p.Text, "\n")
	if n := len(lines); n > 0 {
		lines[n-1] += parts[0]
		parts = parts[1:]
	}
	lines = append(lines, parts...)
	if n := len(lines); n > maxOutputLines {
		lines = append([]string(nil), lines[n-maxOutputLines:]...)
	}
	e.outputs[key] = lines
}

// uiDiagnostics は、バッファの診断を描画の形にする。
func (bs *bufferState) uiDiagnostics() []ui.Diagnostic {
	var out []ui.Diagnostic
	ids := make([]string, 0, len(bs.diagnostics))
	for id := range bs.diagnostics {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, d := range bs.diagnostics[id] {
			out = append(out, ui.Diagnostic{Range: fromExtRange(d.Range), Severity: ui.Severity(d.Severity)})
		}
	}
	return out
}

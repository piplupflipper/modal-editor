package editor

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// 本体の操作を提供者へ振り分ける（基本設計 5.5。FR-94）。
//
// キーマップ（input.DefaultKeymapLeader）とコマンドラインの表（buildExCommands）は、LSP と DAP の操作を
// 拡張機能の ID の代わりに input.ProviderExtension を入れた操作として持つ。
// command.State はそれを拡張機能の操作として extQuery へ渡し、extQuery が呼ばれた時点のバッファのファイルの種類で
// 提供者を選んで provider/invoke を送る（providerAction と providerOperator）。
// 診断の一覧と移動は、本体が受け取った診断から作り、提供者へ問い合わせない（diagnosticAction）。

// errNoProvider は、操作の提供者が無いことを利用者へ知らせる文言を作る。Vim に対応するエラーは無いので番号は付けない。
// command.ErrFailed（ビープだけで知らせない失敗）は包まず、メッセージの行に出す。
func errNoProvider(op ext.Operation, fileType string) error {
	if fileType == "" {
		return fmt.Errorf("%s の提供者がありません", op)
	}
	return fmt.Errorf("%s の提供者がありません（ファイルの種類 %s）", op, fileType)
}

// providerParams は、提供者へ振り分ける操作の ID を、操作の名前と Args にする。
// ID が input.ProviderPeekPrefix で始まれば、Args の peek を true にする。text は、コマンドラインから呼んだときの引数である。
func providerParams(id, text string) (ext.Operation, json.RawMessage) {
	op, peek := strings.CutPrefix(id, input.ProviderPeekPrefix)
	if !peek && text == "" {
		return ext.Operation(op), nil
	}
	b, _ := json.Marshal(ext.ProviderArgs{Peek: peek, Text: text})
	return ext.Operation(op), b
}

// provider は、操作 op を、カーソルのあるバッファ（x.bs）で引き受ける拡張機能を返す（exthost.Host.Provider）。
func (x *extQuery) provider(op ext.Operation) (string, error) {
	id, ok := x.e.host.Provider(op, x.bs.fileType)
	if !ok {
		return "", errNoProvider(op, x.bs.fileType)
	}
	return id, nil
}

// providerAction は、キーかコマンドラインから呼んだ、提供者へ振り分ける操作を行う。
// 結果の扱いは command/execute（Action）と同じである。
//
// req: FR-94
func (x *extQuery) providerAction(ref editcmd.Ref, b *buffer.Buffer, q command.ActionQuery, info command.QueryInfo) (buffer.Transaction, error) {
	if done, err := x.diagnosticAction(ref.ID, b); done {
		return buffer.Transaction{BaseVersion: b.Version()}, err
	}
	op, args := providerParams(ref.ID, q.Text)
	extID, err := x.provider(op)
	if err != nil {
		return buffer.Transaction{}, err
	}
	p := ext.ProviderInvokeParams{
		Operation: op, Buffer: x.bs.id, Version: ext.Version(b.Version()),
		Cursor: toExtPos(q.Cursor), Count: count(q.Count, q.CountGiven), Args: args, Sync: info.Replaying,
	}
	return x.invokeResult(editcmd.Ref{Extension: extID, ID: string(op)}, ext.MethodProviderInvoke, p, b, q, info)
}

// providerOperator は、オペレーターとして呼んだ、提供者へ振り分ける操作（gq{モーション} の format）を行う。
// 範囲を ProviderInvokeParams.Range で渡す。提供者が後から buffer/edit で届ける編集は、ここでは空のトランザクションとして返す。
//
// req: FR-94
func (x *extQuery) providerOperator(ref editcmd.Ref, b *buffer.Buffer, q command.OperatorQuery, info command.QueryInfo) (buffer.Transaction, error) {
	op, args := providerParams(ref.ID, "")
	extID, err := x.provider(op)
	if err != nil {
		return buffer.Transaction{}, err
	}
	rg := toExtRange(q.Span.Range(b))
	p := ext.ProviderInvokeParams{
		Operation: op, Buffer: x.bs.id, Version: ext.Version(b.Version()),
		Cursor: toExtPos(q.Cursor), Range: &rg, Count: q.Count, Args: args, Sync: info.Replaying,
	}
	return x.invokeResult(editcmd.Ref{Extension: extID, ID: string(op)}, ext.MethodProviderInvoke, p, b,
		command.ActionQuery{Cursor: q.Cursor, Count: q.Count}, info)
}

// diagnosticAction は、診断の一覧と移動の操作（input.ProviderDiagnosticList など）を、本体が受け取った診断で行う（基本設計 5.5）。
// id がそれらでなければ done は false である。
//
// 一覧は、一覧の部品（FR-105）ができるまで、カーソルの近くのポップアップ（popup/show と同じ表示）にテキストで出す（仮）。
// 次と前の診断への移動は、診断の範囲の開始の位置の順で、カーソルより後（前）の最初の位置へ移る。端で折り返す（nvim の vim.diagnostic.jump の既定と同じ）。
//
// req: FR-94
func (x *extQuery) diagnosticAction(id string, b *buffer.Buffer) (done bool, err error) {
	switch id {
	case input.ProviderDiagnosticList:
		x.e.showDiagnosticList([]*bufferState{x.bs})
	case input.ProviderDiagnosticListAll:
		x.e.showDiagnosticList(x.e.bufs)
	case input.ProviderDiagnosticNext, input.ProviderDiagnosticPrev:
		pos, ok := nextDiagnostic(x.bs, x.bs.state.Cursor(), id == input.ProviderDiagnosticNext)
		if !ok {
			return true, errors.New("診断がありません")
		}
		if _, err := b.Slice(buffer.Range{Start: pos, End: pos}); err != nil {
			// 受け取った後の編集でずらした位置は境界にあるはずなので、ここに来るのは内部の誤りである。
			return true, fmt.Errorf("%w: diagnostic position %v is outside the buffer", command.ErrFailed, pos)
		}
		x.bs.state.SetCursor(pos)
	default:
		return false, nil
	}
	return true, nil
}

// diagnosticStarts は、bs の診断の範囲の開始の位置を、重なりを除いて昇順に返す。
func diagnosticStarts(bs *bufferState) []buffer.Position {
	var ps []buffer.Position
	for _, id := range slices.Sorted(maps.Keys(bs.diagnostics)) {
		for _, d := range bs.diagnostics[id] {
			s := d.Range.Start
			if d.Range.End.Line < s.Line || (d.Range.End.Line == s.Line && d.Range.End.Byte < s.Byte) {
				s = d.Range.End
			}
			ps = append(ps, fromExtPos(s))
		}
	}
	slices.SortFunc(ps, func(a, b buffer.Position) int {
		switch {
		case a.Less(b):
			return -1
		case b.Less(a):
			return 1
		}
		return 0
	})
	return slices.Compact(ps)
}

// nextDiagnostic は、cur より後（forward が false なら前）の最初の診断の開始の位置を返す。無ければ端で折り返す。
func nextDiagnostic(bs *bufferState, cur buffer.Position, forward bool) (buffer.Position, bool) {
	ps := diagnosticStarts(bs)
	if len(ps) == 0 {
		return buffer.Position{}, false
	}
	if forward {
		for _, p := range ps {
			if cur.Less(p) {
				return p, true
			}
		}
		return ps[0], true
	}
	for i := len(ps) - 1; i >= 0; i-- {
		if ps[i].Less(cur) {
			return ps[i], true
		}
	}
	return ps[len(ps)-1], true
}

// showDiagnosticList は、bss の診断の一覧を、カーソルの近くのポップアップにテキストで出す（仮）。
// 1 行が 1 つの診断で、「パス:行:列: 重要度 出どころ: 本文」の形にする。行と列は 1 から数え、列はバイト数である。
// 診断が無ければメッセージの行に出す。
func (e *Editor) showDiagnosticList(bss []*bufferState) {
	var lines []string
	for _, bs := range bss {
		name := bs.path
		if name == "" {
			name = fmt.Sprintf("[バッファ %d]", bs.id)
		}
		for _, id := range slices.Sorted(maps.Keys(bs.diagnostics)) {
			for _, d := range bs.diagnostics[id] {
				text := strings.Join(strings.Fields(d.Message), " ")
				if d.Source != "" {
					text = d.Source + ": " + text
				}
				lines = append(lines, fmt.Sprintf("%s:%d:%d: %s %s", name, d.Range.Start.Line+1, d.Range.Start.Byte+1, d.Severity, text))
			}
		}
	}
	if len(lines) == 0 {
		e.message = infoMessage("診断はありません")
		return
	}
	slices.Sort(lines)
	cur := e.cur
	e.popup = &popupState{bs: cur, cursor: cur.state.Cursor(), text: strings.Join(lines, "\n")}
}

// providerExCommands は、拡張機能が provides で名乗った操作を、コマンドラインから操作の名前で呼ぶための表に足す（:rename 新しい名前）。
// 拡張機能のコマンド（contributes.commands）と同じ名前の操作は足さない（拡張機能のコマンドを優先する）。
func providerExCommands(cmds map[string]string, specs []ext.Manifest) {
	for _, m := range specs {
		for _, op := range m.Provides {
			if !op.Known() {
				continue
			}
			if _, ok := cmds[string(op)]; !ok {
				cmds[string(op)] = input.ProviderExtension
			}
		}
	}
}

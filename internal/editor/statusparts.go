package editor

import (
	"fmt"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// この節は、拡張機能がステータス行とタブ行に足す部品（宣言ファイルの contributes.statusline と statusline/set）を持つ
// （基本設計 7.1。FR-92）。

// buildStatusOwners は、拡張機能が宣言した部品の ID から、宣言した拡張機能の ID への表を作る。
// 部品の ID は拡張機能の ID で始まる（ext.Manifest.Validate）ので、2 つの拡張機能の部品は重ならない。
func buildStatusOwners(specs []exthost.Spec) map[string]string {
	owners := map[string]string{}
	for _, s := range specs {
		for _, p := range s.Manifest.Contributes.StatusLine {
			owners[p.ID] = s.Manifest.ID
		}
	}
	return owners
}

// layoutExtParts は、設定のステータス行とタブ行の並びに書いた、本体の部品でない ID（拡張機能の部品の ID）を、並べた順に重複なく返す。
func layoutExtParts(status, tab *ui.StatusLine) []string {
	sl, tl := ui.DefaultStatusLine(), ui.DefaultTabLineParts()
	if status != nil {
		sl = *status
	}
	if tab != nil {
		tl = *tab
	}
	var out []string
	seen := map[string]bool{}
	for _, ids := range [][]string{sl.Left, sl.Right, tl.Left, tl.Right} {
		for _, id := range ids {
			if ui.IsTabLinePart(id) || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// startStatusExtensions は、設定の並びに部品を書いた拡張機能を、起動の契機を待たずに起動する。
// 部品の中身は拡張機能が送るまで無いので、並べた部品を出すために起動時に動かす（lualine の部品が描くたびに評価されるのに当たる）。
// 並べた ID を足す拡張機能が無ければ、誤りとして返す（部品は出さない）。
func (e *Editor) startStatusExtensions() []error {
	var errs []error
	started := map[string]bool{}
	for _, id := range layoutExtParts(e.cfg.StatusLine, e.cfg.TabLineParts) {
		owner, ok := e.statusOwners[id]
		if !ok {
			errs = append(errs, fmt.Errorf("statusline か tabline の部品 %s は、本体の部品でも拡張機能の部品（contributes.statusline）でもないので出さない", id))
			continue
		}
		if started[owner] {
			continue
		}
		started[owner] = true
		if err := e.host.Start(owner); err != nil {
			errs = append(errs, fmt.Errorf("部品 %s の拡張機能 %s を起動できない: %w", id, owner, err))
		}
	}
	return errs
}

// setStatusPart は、statusline/set の部品の中身を持つ（ext.MethodStatusLineSet）。
// 送った拡張機能が宣言していない部品は捨てる。Text が空なら部品の中身を消す（部品を出さない）。
//
// req: FR-92
func (e *Editor) setStatusPart(extID string, p ext.StatusLineSetParams) {
	if e.statusOwners[p.Part] != extID {
		e.log.Warn("宣言していない部品の statusline/set を捨てる", "extension", extID, "part", p.Part)
		return
	}
	text := ext.StatusLineText(p.Text)
	if text == "" {
		delete(e.extParts, p.Part)
		return
	}
	e.extParts[p.Part] = ui.ExtPart{Text: text, Style: p.Style}
}

// clearStatusParts は、拡張機能 extID の部品の中身をすべて消す。拡張機能が異常で終わったか止まったときに呼ぶ（clearExtension）。
func (e *Editor) clearStatusParts(extID string) {
	for id := range e.extParts {
		if e.statusOwners[id] == extID {
			delete(e.extParts, id)
		}
	}
}

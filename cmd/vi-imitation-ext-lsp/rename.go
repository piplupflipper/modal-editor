package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/piplupflipper/modal-editor/ext"
)

// 名前の変更（textDocument/rename）の適用。全部のファイルを変えるか、何も変えないかにする（基本設計 4.4、6.3。#237）。
//
//  1. 言語サーバーの WorkspaceEdit を文書ごとの編集にする。同期していないファイル（本体で開いていないファイル）は、
//     editor/open（focus が false）で本体の画面に出さないバッファとして開く（FR-109。openForRename）
//  2. 編集を始める前に、対象のファイルがすべて編集できるか（開けない、編集の禁止、外で変更された、名前の変更の間に変わった）を
//     editor/state で確かめる（checkRename）。1 つでもだめなら何も変えずに、理由のファイルを知らせる
//  3. タブのバッファ、画面に出さないバッファの順に buffer/edit を送る（applyRename）。途中で断られたら、それまでに変えたバッファを
//     元の全文に戻し（rollbackRename）、知らせる
//  4. 知らせを出した後に、開いたファイルへ buffer/save を送る（finishRename）。本体は書き出して閉じ、変えたファイルの一覧を出す
//
// 開いたが変えなかったファイル、元に戻したファイルにも buffer/save を送る。本体は、画面に出さないバッファの中身がファイルと同じなら、
// 書き出さずに閉じる（internal/editor の bufferSaveRequest）。これで、変えていないバッファを残さない。

// renameTarget は、名前の変更で編集する 1 つのバッファである。
type renameTarget struct {
	params ext.BufferEditParams
	// path は、ファイルの絶対パスである。
	path string
	// before は、編集の前の行（params の BaseVersion の行）である。元に戻すときに使う。
	before lines
	// opened は、名前の変更のために画面に出さないバッファとして開いたものかである。編集の後に書き出させる。
	opened bool
}

// renamePlan は、確かめ終えた名前の変更である。
type renamePlan struct {
	targets []renameTarget
	// opened は、名前の変更のために画面に出さないバッファとして開いたすべてのバッファ（編集が無かったものを含む）である。
	opened []ext.BufferID
	// root は、知らせに出すパスを相対にする基点（ワークスペースの根）である。
	root string
	// versions は、applyRename が編集した後のバッファのバージョン番号である。書き出させるときに、その版のときだけ書くよう渡す。
	versions map[ext.BufferID]ext.Version
}

// renameResult は、textDocument/rename の応答を待ち、確かめ終えた名前の変更にする。
// 変更する箇所が無ければ、そのことを知らせて空の plan と true を返す。
// 応答を得られないか、適用できない形（ファイルの作成などを含む、編集できないファイルに及ぶ）なら、理由を知らせて false を返す。
// このとき、開いたファイルは閉じさせ、どのバッファも変えない。
// ctx はハンドラーの ctx でもよい（本体へのリクエストは、background の ctx で送る）。
func (x *extension) renameResult(ctx context.Context, r *request) (*renamePlan, bool) {
	var raw json.RawMessage
	if err := r.wait(ctx, &raw); err != nil {
		x.logf("%s: %v", lspRename, err)
		x.showMessage(ext.MessageError, fmt.Sprintf("名前を変更できない: %v", err))
		return nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		x.showMessage(ext.MessageInfo, "名前を変更する箇所が無い")
		return &renamePlan{}, true
	}
	var w workspaceEdit
	if err := json.Unmarshal(raw, &w); err != nil {
		x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更の結果を読めない: %v", err))
		return nil, false
	}
	targets, unopened, err := r.c.renameEdits(&w, r.enc)
	if err != nil {
		x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更を適用できない: %v", err))
		return nil, false
	}
	r.c.mu.Lock()
	plan := &renamePlan{targets: targets, root: r.c.root}
	r.c.mu.Unlock()
	if len(targets) == 0 && len(unopened) == 0 {
		return plan, true
	}
	bctx, cancel := x.background()
	defer cancel()
	problems := x.openForRename(bctx, plan, unopened, r.enc)
	if len(problems) == 0 {
		problems = x.checkRename(bctx, plan)
	}
	if len(problems) > 0 {
		x.closeOpened(bctx, plan)
		x.showMessage(ext.MessageWarning, "名前の変更を適用しなかった（どのファイルも変えていない）: "+strings.Join(problems, "、"))
		return nil, false
	}
	return plan, true
}

// openForRename は、名前の変更が及ぶ、この拡張機能が同期していないファイル unopened（パスから LSP の編集の列へ）を、
// editor/open（focus が false）で本体に開かせ、届いた複製の版の対象にして plan に足す（FR-109）。
// 開く前に本体で開いていなかったファイルは、本体が画面に出さないバッファにするので、opened にする（編集の後に書き出させる）。
// 本体で既に開いているファイル（この拡張機能の言語サーバーの対象でない種類のバッファなど）は、タブのバッファと同じく書き出さない。
// 開けなかったファイルを、知らせに出す形（「パス（理由）」）の列で返す。ループの外の goroutine から呼ぶ。
func (x *extension) openForRename(ctx context.Context, plan *renamePlan, unopened map[string][]textEdit, enc encoding) []string {
	if len(unopened) == 0 {
		return nil
	}
	st, err := x.srv.EditorState(ctx)
	if err != nil {
		return []string{fmt.Sprintf("editor/state（%v）", err)}
	}
	open := map[string]bool{}
	for _, b := range st.Buffers {
		open[b.Path] = true
	}
	var problems []string
	for _, path := range slices.Sorted(maps.Keys(unopened)) {
		res, err := x.srv.OpenEditor(ctx, ext.EditorOpenParams{Path: path, Focus: false})
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s（開けない: %v）", plan.rel(path), err))
			continue
		}
		if !open[path] {
			plan.opened = append(plan.opened, res.Buffer)
		}
		b, err := x.srv.WaitBuffer(ctx, res.Buffer, res.Version)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s（開けない: %v）", plan.rel(path), err))
			continue
		}
		ls := make(lines, b.LineCount())
		for i := range ls {
			ls[i] = b.Line(i)
		}
		t := formattingTransaction(ls, b.FinalNewline(), b.Version(), unopened[path], enc)
		if t == nil {
			continue
		}
		plan.targets = append(plan.targets, renameTarget{
			params: ext.BufferEditParams{Buffer: res.Buffer, Transaction: *t}, path: path, before: ls, opened: !open[path],
		})
	}
	return problems
}

// checkRename は、編集を始める前に、plan の対象がすべて編集できるかを editor/state で確かめ、だめなものを
// 知らせに出す形（「パス（理由）」）の列で返す。理由は、開いていない、編集の禁止、外で変更された（書き出させるものだけ）、
// 名前の変更の間に変わった（バージョン番号が編集の元と違う）である。
func (x *extension) checkRename(ctx context.Context, plan *renamePlan) []string {
	st, err := x.srv.EditorState(ctx)
	if err != nil {
		return []string{fmt.Sprintf("editor/state（%v）", err)}
	}
	infos := map[ext.BufferID]ext.BufferInfo{}
	for _, b := range st.Buffers {
		infos[b.Buffer] = b
	}
	var problems []string
	for _, t := range plan.targets {
		info, ok := infos[t.params.Buffer]
		var why string
		switch {
		case !ok:
			why = "開いていない"
		case info.ReadOnly:
			why = "編集の禁止"
		case t.opened && info.ChangedOnDisk:
			why = "外で変更された"
		case info.Version != t.params.Transaction.BaseVersion:
			why = "名前の変更の間に変わった"
		default:
			continue
		}
		problems = append(problems, fmt.Sprintf("%s（%s）", plan.rel(t.path), why))
	}
	return problems
}

// applyRename は、plan の編集を、タブのバッファ、画面に出さないバッファの順に buffer/edit で適用する。
// 途中で断られたら、それまでに変えたバッファを元に戻し、開いたファイルを閉じさせ、知らせて false を返す。
func (x *extension) applyRename(ctx context.Context, plan *renamePlan) bool {
	order := slices.Clone(plan.targets)
	slices.SortStableFunc(order, func(a, b renameTarget) int {
		switch {
		case a.opened == b.opened:
			return 0
		case !a.opened:
			return -1
		}
		return 1
	})
	type done struct {
		t       renameTarget
		version ext.Version
	}
	var applied []done
	for _, t := range order {
		res, err := x.srv.Edit(ctx, t.params)
		why := ""
		switch {
		case err != nil:
			x.logf("buffer/edit: %v", err)
			why = err.Error()
		case !res.Applied:
			why = "名前の変更の間に変わった"
		}
		if why == "" {
			applied = append(applied, done{t, res.Version})
			if plan.versions == nil {
				plan.versions = map[ext.BufferID]ext.Version{}
			}
			plan.versions[t.params.Buffer] = res.Version
			continue
		}
		var failed []string
		for i := len(applied) - 1; i >= 0; i-- {
			if err := x.rollbackRename(ctx, applied[i].t, applied[i].version); err != nil {
				x.logf("rename rollback: %v", err)
				failed = append(failed, plan.rel(applied[i].t.path))
			}
		}
		x.closeOpened(ctx, plan)
		msg := fmt.Sprintf("名前の変更の途中で %s を変えられなかったので（%s）、変えた %d 個のファイルを元に戻した", plan.rel(t.path), why, len(applied)-len(failed))
		if len(failed) > 0 {
			msg += "。元に戻せなかった: " + strings.Join(failed, "、")
		}
		x.showMessage(ext.MessageWarning, msg)
		return false
	}
	return true
}

// rollbackRename は、名前の変更で版 version にしたバッファ t を、編集の前の全文（t.before）に戻す。
func (x *extension) rollbackRename(ctx context.Context, t renameTarget, version ext.Version) error {
	b, err := x.srv.WaitBuffer(ctx, t.params.Buffer, version)
	if err != nil {
		return err
	}
	last := b.LineCount() - 1
	res, err := x.srv.Edit(ctx, ext.BufferEditParams{Buffer: t.params.Buffer, Transaction: ext.Transaction{
		BaseVersion: version,
		Edits:       []ext.Edit{{Range: ext.Range{End: ext.Position{Line: last, Byte: len(b.Line(last))}}, Text: strings.Join(t.before, "\n")}},
	}})
	switch {
	case err != nil:
		return err
	case !res.Applied && res.Version != version:
		return fmt.Errorf("buffer %d changed during the rollback", t.params.Buffer)
	}
	return nil
}

// closeOpened は、名前の変更のために開いた画面に出さないバッファに buffer/save を送る。
// 変えていないか元に戻したバッファは、中身がファイルと同じなので、本体は書き出さずに閉じる。
func (x *extension) closeOpened(ctx context.Context, plan *renamePlan) {
	for _, id := range plan.opened {
		if _, err := x.srv.SaveBuffer(ctx, ext.BufferSaveParams{Buffer: id, SkipHooks: true}); err != nil {
			x.logf("buffer/save: %v", err)
		}
	}
}

// finishRename は、適用し終えた名前の変更を知らせ（n はファイルの数）、その後に、開いたファイルを buffer/save で書き出させる。
// 本体は書き出したバッファを閉じ、変えたファイルの一覧をメッセージの行に出す（FR-109）。知らせを先に出すのは、一覧を上書きしないためである。
// 保存の前の整形（buffer/willSave）は通さない（ACP の fs/write_text_file と同じ。基本設計 6.5）。
//
// req: FR-109
func (x *extension) finishRename(ctx context.Context, plan *renamePlan, n int, newName string) {
	x.showMessage(ext.MessageInfo, fmt.Sprintf("名前を %s に変えた（%d ファイル）", newName, n))
	for _, id := range plan.opened {
		p := ext.BufferSaveParams{Buffer: id, SkipHooks: true}
		if v, ok := plan.versions[id]; ok {
			p.Version = &v
		}
		res, err := x.srv.SaveBuffer(ctx, p)
		switch {
		case err != nil:
			x.logf("buffer/save: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("名前を変えた開いていないファイルを書き出せない: %v", err))
		case !res.Saved:
			// 書き出せなかった理由と、残したバッファの開き方は、本体が利用者へ表示している。
			x.logf("buffer/save: buffer %d was not saved", id)
		}
	}
}

// rel は、知らせに出すパスを返す。ワークスペースの根の下なら相対のパスにする。
func (p *renamePlan) rel(path string) string {
	if p.root != "" {
		if r, err := filepath.Rel(p.root, path); err == nil && filepath.IsLocal(r) {
			return r
		}
	}
	return path
}

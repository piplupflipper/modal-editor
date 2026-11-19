package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// 段階 3 で足した本体の操作（基本設計 5.5）のうち、参照の一覧、シンボルの一覧、名前の変更を処理する。
// 一覧は、一覧の部品（FR-105）ができるまで choice/ask で選ばせ、選んだ項目へ editor/open で移る（仮）。

// documentRangeFormattingParams は、textDocument/rangeFormatting の params である（LSP 3.17）。
type documentRangeFormattingParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Range        lspRange               `json:"range"`
	Options      formattingOptions      `json:"options"`
}

// referenceParams は、textDocument/references の params である。宣言も含める。
type referenceParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
	Context      struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

// documentSymbol は、textDocument/documentSymbol の結果の DocumentSymbol と SymbolInformation を 1 つの型で読む。
// DocumentSymbol は selectionRange と children を、SymbolInformation は location を持つ（LSP 3.17）。
type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	SelectionRange *lspRange        `json:"selectionRange"`
	Children       []documentSymbol `json:"children"`
	Location       *struct {
		URI   string    `json:"uri"`
		Range *lspRange `json:"range"`
	} `json:"location"`
	ContainerName string `json:"containerName"`
}

// workspaceSymbolParams は、workspace/symbol の params である。
type workspaceSymbolParams struct {
	Query string `json:"query"`
}

// renameParams は、textDocument/rename の params である。
type renameParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     lspPosition            `json:"position"`
	NewName      string                 `json:"newName"`
}

// workspaceEdit は、WorkspaceEdit のうち、文書の編集（changes と documentChanges の TextDocumentEdit）を読む。
// ファイルの作成、名前の変更、削除（documentChanges の kind のあるもの）は読まず、hasResourceOps で知らせる。
type workspaceEdit struct {
	Changes         map[string][]textEdit `json:"changes"`
	DocumentChanges []json.RawMessage     `json:"documentChanges"`
}

// textDocumentEdit は、TextDocumentEdit である。
type textDocumentEdit struct {
	Kind         string                          `json:"kind"`
	TextDocument versionedTextDocumentIdentifier `json:"textDocument"`
	Edits        []textEdit                      `json:"edits"`
}

// fileEdits は、workspaceEdit を、URI ごとの編集にまとめる。ファイルの作成などの操作を含めば resourceOps は true である。
func (w *workspaceEdit) fileEdits() (edits map[string][]textEdit, resourceOps bool, err error) {
	edits = map[string][]textEdit{}
	for uri, es := range w.Changes {
		edits[uri] = append(edits[uri], es...)
	}
	for _, raw := range w.DocumentChanges {
		var e textDocumentEdit
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, false, err
		}
		if e.Kind != "" {
			resourceOps = true
			continue
		}
		edits[e.TextDocument.URI] = append(edits[e.TextDocument.URI], e.Edits...)
	}
	return edits, resourceOps, nil
}

// symbolKindNames は、SymbolKind の名前である（LSP 3.17 の 1 から 26）。
var symbolKindNames = []string{
	"", "file", "module", "namespace", "package", "class", "method", "property", "field", "constructor",
	"enum", "interface", "function", "variable", "constant", "string", "number", "boolean", "array",
	"object", "key", "null", "enumMember", "struct", "event", "operator", "typeParameter",
}

func symbolKindName(k int) string {
	if k > 0 && k < len(symbolKindNames) {
		return symbolKindNames[k]
	}
	return fmt.Sprint(k)
}

// listResult は、一覧の操作の結果である。
type listResult struct {
	// title は、choice/ask の見出しである。none は、候補が無いときの知らせである。
	title, none string
	targets     []target
}

// runList は、一覧の操作の結果を collect で集め、choice/ask で選ばせ、選んだ候補へ移る（基本設計 5.5）。
// 集めるのに失敗したら、failText に理由を付けて知らせる。
//
// sync が false なら、受け付けたことだけを ok で応答し、集めて選ばせるのは後から行う。
// sync が true（マクロとドットリピートの再生中）なら、集め終えてから応答する（FR-19）。
// 記録したときに選んだ候補はマクロに入らない（choice/ask への答えはマクロに記録されない）ので、選ばせない。
// 候補が 1 つなら、その候補への移動を応答の open で返す。候補が無いか複数なら、そのことを知らせて ok を false にし、再生を止める。
// 記録したときに選んだ候補を再生では決められないので、別の候補へ移って再生を続けるより、止めて利用者に任せる。
func (x *extension) runList(ctx context.Context, sync bool, reply func(ext.ProviderInvokeResult, error), method, failText string,
	collect func(ctx context.Context) (listResult, error),
) {
	if sync {
		go func() {
			res, err := collect(ctx)
			if err != nil {
				x.logf("%s: %v", method, err)
				x.showMessage(ext.MessageError, fmt.Sprintf("%s: %v", failText, err))
				reply(ext.ProviderInvokeResult{OK: false}, nil)
				return
			}
			switch len(res.targets) {
			case 0:
				x.showMessage(ext.MessageInfo, res.none)
				reply(ext.ProviderInvokeResult{OK: false}, nil)
			case 1:
				open := res.targets[0].open
				reply(ext.ProviderInvokeResult{OK: true, Open: &open}, nil)
			default:
				x.showMessage(ext.MessageWarning, fmt.Sprintf("%s: 再生中は候補を選べないので、再生を止めた", res.title))
				reply(ext.ProviderInvokeResult{OK: false}, nil)
			}
		}()
		return
	}
	reply(ext.ProviderInvokeResult{OK: true}, nil)
	go func() {
		ctx, cancel := x.background()
		defer cancel()
		res, err := collect(ctx)
		if err != nil {
			x.logf("%s: %v", method, err)
			x.showMessage(ext.MessageError, fmt.Sprintf("%s: %v", failText, err))
			return
		}
		x.listTargets(res.title, res.none, res.targets)
	}()
}

// listTargets は、一覧の候補を choice/ask で選ばせ、選んだ候補へ移る。候補が無ければ none を知らせる。
func (x *extension) listTargets(title, none string, targets []target) {
	if len(targets) == 0 {
		x.showMessage(ext.MessageInfo, none)
		return
	}
	t, ok := x.pick(title, targets)
	if !ok {
		return
	}
	x.open(t.open, title+"の項目へ移れない")
}

// references は、本体の操作 references を処理する（基本設計 5.5）。
// カーソル位置で textDocument/references を送り、受け付けたことだけを ok で応答する。
// 結果の場所を choice/ask で選ばせ、選んだ場所へ移る。再生中（sync）の扱いは runList にある。
//
// req: FR-94, FR-19
func (x *extension) references(ctx context.Context, _ *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
	cursor := p.Cursor
	r, err := x.sendFor(p, "参照の一覧", lspReferences,
		func(c *serverCapabilities) bool { return provides(c.ReferencesProvider) },
		func(uri string, ls lines, enc encoding) any {
			rp := referenceParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: ls.toLSP(cursor, enc)}
			rp.Context.IncludeDeclaration = true
			return rp
		})
	if err != nil {
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	x.runList(ctx, p.Sync, reply, lspReferences, "参照の一覧を得られない", func(ctx context.Context) (listResult, error) {
		targets, err := x.locationTargets(ctx, r)
		if err != nil {
			return listResult{}, err
		}
		for i := range targets {
			if len(targets[i].lines) > 0 {
				targets[i].label += ": " + strings.TrimSpace(targets[i].lines[0])
			}
		}
		return listResult{title: fmt.Sprintf("参照（%d 件）", len(targets)), none: "参照が見つからない", targets: targets}, nil
	})
}

// documentSymbols は、本体の操作 documentSymbols を処理する（基本設計 5.5）。
// textDocument/documentSymbol の結果（DocumentSymbol の木か SymbolInformation の列）を、
// 「親.名前 (種類) 行」の列にして choice/ask で選ばせ、選んだシンボルへ移る。再生中（sync）の扱いは runList にある。
//
// req: FR-94, FR-19
func (x *extension) documentSymbols(ctx context.Context, _ *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
	r, err := x.sendFor(p, "シンボルの一覧", lspDocumentSymbol,
		func(c *serverCapabilities) bool { return provides(c.DocumentSymbolProvider) },
		func(uri string, _ lines, _ encoding) any {
			return struct {
				TextDocument textDocumentIdentifier `json:"textDocument"`
			}{textDocumentIdentifier{URI: uri}}
		})
	if err != nil {
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	x.runList(ctx, p.Sync, reply, lspDocumentSymbol, "シンボルの一覧を得られない", func(ctx context.Context) (listResult, error) {
		var syms []documentSymbol
		if err := r.wait(ctx, &syms); err != nil {
			return listResult{}, err
		}
		return listResult{title: "シンボル", none: "シンボルが無い", targets: symbolTargets(r, syms)}, nil
	})
}

// symbolTargets は、documentSymbol の列を、問い合わせた文書の中の移動の候補にする。木は深さ優先で平らにする。
func symbolTargets(r *request, syms []documentSymbol) []target {
	var out []target
	var walk func(prefix string, ss []documentSymbol)
	walk = func(prefix string, ss []documentSymbol) {
		for _, s := range ss {
			var start *lspPosition
			switch {
			case s.SelectionRange != nil:
				start = &s.SelectionRange.Start
			case s.Location != nil && s.Location.Range != nil:
				start = &s.Location.Range.Start
			}
			name := prefix + s.Name
			if prefix == "" && s.ContainerName != "" {
				name = s.ContainerName + "." + s.Name
			}
			if start != nil {
				at, _ := r.lines.fromLSP(*start, r.enc)
				id, v := r.buffer, r.version
				out = append(out, target{
					open:  ext.EditorOpenParams{Buffer: &id, Version: &v, Position: &at, Focus: true},
					label: fmt.Sprintf("%s (%s) %d", name, symbolKindName(s.Kind), at.Line+1),
				})
			}
			walk(name+".", s.Children)
		}
	}
	walk("", syms)
	return out
}

// workspaceSymbols は、本体の操作 workspaceSymbols を処理する（基本設計 5.5）。
// 問い合わせの文字列は、コマンドラインの引数（:workspaceSymbols 名前）か、無ければカーソルの位置の単語にする。
// workspace/symbol の結果を choice/ask で選ばせ、選んだシンボルへ移る。再生中（sync）の扱いは runList にある。
//
// req: FR-94, FR-19
func (x *extension) workspaceSymbols(ctx context.Context, b *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
	query := strings.TrimSpace(providerArgs(p).Text)
	if query == "" && b != nil {
		query = wordAt(b.Line(p.Cursor.Line), p.Cursor.Byte)
	}
	r, err := x.sendFor(p, "ワークスペースのシンボルの一覧", lspWorkspaceSymbol,
		func(c *serverCapabilities) bool { return provides(c.WorkspaceSymbolProvider) },
		func(string, lines, encoding) any { return workspaceSymbolParams{Query: query} })
	if err != nil {
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	x.runList(ctx, p.Sync, reply, lspWorkspaceSymbol, "シンボルの一覧を得られない", func(ctx context.Context) (listResult, error) {
		var syms []documentSymbol
		if err := r.wait(ctx, &syms); err != nil {
			return listResult{}, err
		}
		var targets []target
		for _, s := range syms {
			if s.Location == nil {
				continue
			}
			path, ok := uriToPath(s.Location.URI)
			if !ok {
				continue
			}
			var pos lspPosition
			if s.Location.Range != nil {
				pos = s.Location.Range.Start
			}
			t := r.c.resolve(r, path, pos)
			name := s.Name
			if s.ContainerName != "" {
				name = s.ContainerName + "." + name
			}
			t.label = fmt.Sprintf("%s (%s) %s", name, symbolKindName(s.Kind), t.label)
			targets = append(targets, t)
		}
		return listResult{title: fmt.Sprintf("シンボル %q", query), none: fmt.Sprintf("シンボル %q が見つからない", query), targets: targets}, nil
	})
}

// wordAt は、line のバイト位置 at にある識別子（文字、数字、_ の並び）を返す。無ければ空である。
func wordAt(line string, at int) string {
	isWord := func(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
	if at > len(line) {
		at = len(line)
	}
	start := at
	for start > 0 {
		r, n := utf8.DecodeLastRuneInString(line[:start])
		if !isWord(r) {
			break
		}
		start -= n
	}
	end := at
	for end < len(line) {
		r, n := utf8.DecodeRuneInString(line[end:])
		if !isWord(r) {
			break
		}
		end += n
	}
	return line[start:end]
}

// rename は、本体の操作 rename を処理する（基本設計 5.5）。
// 新しい名前はコマンドラインの引数（:rename 新しい名前）で受け取る。キーで呼ばれて引数が無ければ、引数の渡し方を知らせて失敗する
// （入力の行の部品（基本設計 1.4）ができるまでの仮の形）。
//
// textDocument/rename の WorkspaceEdit を、文書ごとのトランザクションにして buffer/edit で適用する。
// 編集が、この拡張機能が同期していないファイル（本体で開いていないファイル）に及ぶときは、そのファイルを editor/open（focus が false）で
// 本体の画面に出さないバッファとして開いてから編集し、buffer/save で書き出させる（FR-109。基本設計 4.4、6.3）。
// 本体は書き出した後にそのバッファを閉じ、変えたファイルの一覧をメッセージの行に出す。
// 開けないファイルがあるか、ファイルの作成などを含むときは、何も適用せずに知らせる。一部のファイルだけを変えると、コードが壊れたまま残るためである。
//
// sync が true（マクロとドットリピートの再生中）なら、結果がそろってから応答し、問い合わせたバッファの編集を応答の transaction で返す
// （基本設計 6 章の冒頭。後から buffer/edit で届けると、再生の次の操作でバージョン番号が進んだ後に届いて破棄される）。
// 他のバッファの編集は、応答の後に buffer/edit で届ける（本体は応答を待つ間も buffer/edit を処理するが、再生の続きの後に届いても困らない）。
// 結果を得られないか適用できなければ、ok を false にして再生を止める。
//
// req: FR-94, FR-19, FR-109
func (x *extension) rename(ctx context.Context, _ *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
	newName := strings.TrimSpace(providerArgs(p).Text)
	if newName == "" {
		x.showMessage(ext.MessageWarning, "新しい名前を :rename 新しい名前 で渡す")
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	cursor := p.Cursor
	r, err := x.sendFor(p, "名前の変更", lspRename,
		func(c *serverCapabilities) bool { return provides(c.RenameProvider) },
		func(uri string, ls lines, enc encoding) any {
			return renameParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: ls.toLSP(cursor, enc), NewName: newName}
		})
	if err != nil {
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	if p.Sync {
		go func() {
			edits, saves, ok := x.renameResult(ctx, r)
			if !ok {
				reply(ext.ProviderInvokeResult{OK: false}, nil)
				return
			}
			// 問い合わせたバッファの編集は応答で返し、他のバッファの編集は応答の後に送る。
			var own *ext.Transaction
			rest := make([]ext.BufferEditParams, 0, len(edits))
			for _, e := range edits {
				if e.Buffer == r.buffer && own == nil {
					t := e.Transaction
					own = &t
					continue
				}
				rest = append(rest, e)
			}
			reply(ext.ProviderInvokeResult{OK: true, Transaction: own}, nil)
			if len(edits) == 0 {
				return
			}
			done := 0
			if own != nil {
				done = 1
			}
			bctx, cancel := x.background()
			defer cancel()
			x.applyRenameEdits(bctx, rest, saves, done, newName)
		}()
		return
	}
	reply(ext.ProviderInvokeResult{OK: true}, nil)
	go func() {
		ctx, cancel := x.background()
		defer cancel()
		edits, saves, ok := x.renameResult(ctx, r)
		if ok && len(edits) > 0 {
			x.applyRenameEdits(ctx, edits, saves, 0, newName)
		}
	}()
}

// renameResult は、textDocument/rename の応答を待ち、文書ごとの buffer/edit の params にする。
// 同期していないファイルは、openForRename で本体の画面に出さないバッファとして開いてから params にし、
// そのうち書き出させるバッファ（それまで本体で開いていなかったもの）を saves に入れて返す。
// 変更する箇所が無ければ、そのことを知らせて空の列と true を返す。
// 応答を得られないか、適用できない形（開けないファイルに及ぶもの、ファイルの作成などを含むもの）なら、理由を知らせて false を返す。
// ctx はハンドラーの ctx でもよい（本体へのリクエストは、background の ctx で送る）。
func (x *extension) renameResult(ctx context.Context, r *request) ([]ext.BufferEditParams, map[ext.BufferID]bool, bool) {
	var raw json.RawMessage
	if err := r.wait(ctx, &raw); err != nil {
		x.logf("%s: %v", lspRename, err)
		x.showMessage(ext.MessageError, fmt.Sprintf("名前を変更できない: %v", err))
		return nil, nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		x.showMessage(ext.MessageInfo, "名前を変更する箇所が無い")
		return nil, nil, true
	}
	var w workspaceEdit
	if err := json.Unmarshal(raw, &w); err != nil {
		x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更の結果を読めない: %v", err))
		return nil, nil, false
	}
	edits, unopened, err := r.c.renameEdits(&w, r.enc)
	if err != nil {
		x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更を適用できない: %v", err))
		return nil, nil, false
	}
	if len(unopened) == 0 {
		return edits, nil, true
	}
	bctx, cancel := x.background()
	defer cancel()
	more, saves, err := x.openForRename(bctx, unopened, r.enc)
	if err != nil {
		x.logf("%s: %v", lspRename, err)
		x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更は開いていないファイルに及ぶが、そのファイルを開けないので、適用しなかった: %v", err))
		return nil, nil, false
	}
	return append(edits, more...), saves, true
}

// openForRename は、名前の変更が及ぶ、この拡張機能が同期していないファイル unopened（パスから LSP の編集の列へ）を、
// editor/open（focus が false）で本体に開かせ、届いた複製の版の buffer/edit の params にする（FR-109）。
// 開く前に本体で開いていなかったファイルは、本体が画面に出さないバッファにするので、書き出させる（saves）。
// 本体で既に開いているファイル（この拡張機能の言語サーバーの対象でない種類のバッファなど）は、タブのバッファと同じく書き出さない。
// どれかを開けなければ、誤りを返す。それまでに開いたファイルは、編集せずに残る（基本設計 4.4）。
// ループの外の goroutine から呼ぶ。
func (x *extension) openForRename(ctx context.Context, unopened map[string][]textEdit, enc encoding) ([]ext.BufferEditParams, map[ext.BufferID]bool, error) {
	st, err := x.srv.EditorState(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("editor/state: %w", err)
	}
	open := map[string]bool{}
	for _, b := range st.Buffers {
		open[b.Path] = true
	}
	var out []ext.BufferEditParams
	saves := map[ext.BufferID]bool{}
	for _, path := range slices.Sorted(maps.Keys(unopened)) {
		res, err := x.srv.OpenEditor(ctx, ext.EditorOpenParams{Path: path, Focus: false})
		if err != nil {
			return nil, nil, fmt.Errorf("%s: editor/open: %w", path, err)
		}
		b, err := x.srv.WaitBuffer(ctx, res.Buffer, res.Version)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		ls := make(lines, b.LineCount())
		for i := range ls {
			ls[i] = b.Line(i)
		}
		t := formattingTransaction(ls, b.FinalNewline(), b.Version(), unopened[path], enc)
		if t == nil {
			continue
		}
		out = append(out, ext.BufferEditParams{Buffer: res.Buffer, Transaction: *t})
		if !open[path] {
			saves[res.Buffer] = true
		}
	}
	return out, saves, nil
}

// applyRenameEdits は、名前の変更の編集を buffer/edit で順に適用し、適用したファイルの数を知らせる。
// done は、先に適用したファイルの数（再生中に応答で返したもの）である。
// 知らせた後に、saves のバッファ（画面に出さないバッファ）を buffer/save で書き出させる。本体は書き出したバッファを閉じ、
// 変えたファイルの一覧をメッセージの行に出す（FR-109）。知らせを先に出すのは、一覧を上書きしないためである。
// 保存の前の整形（buffer/willSave）は通さない（ACP の fs/write_text_file と同じ。基本設計 6.5）。
//
// req: FR-109
func (x *extension) applyRenameEdits(ctx context.Context, edits []ext.BufferEditParams, saves map[ext.BufferID]bool, done int, newName string) {
	n := done
	versions := map[ext.BufferID]ext.Version{}
	for _, e := range edits {
		res, err := x.srv.Edit(ctx, e)
		switch {
		case err != nil:
			x.logf("buffer/edit: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("名前の変更を適用できない: %v", err))
			return
		case !res.Applied:
			x.showMessage(ext.MessageWarning, "名前の変更の間にバッファが変わったので、残りを適用しなかった")
			return
		}
		versions[e.Buffer] = res.Version
		n++
	}
	x.showMessage(ext.MessageInfo, fmt.Sprintf("名前を %s に変えた（%d ファイル）", newName, n))
	for _, id := range slices.Sorted(maps.Keys(versions)) {
		if !saves[id] {
			continue
		}
		v := versions[id]
		res, err := x.srv.SaveBuffer(ctx, ext.BufferSaveParams{Buffer: id, Version: &v, SkipHooks: true})
		switch {
		case err != nil:
			x.logf("buffer/save: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("名前を変えた開いていないファイルを書き出せない: %v", err))
		case !res.Saved:
			// 書き出せなかった理由は、本体が利用者へ表示している。
			x.logf("buffer/save: buffer %d was not saved", id)
		}
	}
}

// renameEdits は、WorkspaceEdit を、同期している文書ごとの buffer/edit の params にする。
// 同期していないファイルの編集は、パスごとに unopened に入れて返す（呼び出し側が本体に開かせる。openForRename）。
// ファイルでない URI か、ファイルの作成などを含めば誤りを返す。
func (c *client) renameEdits(w *workspaceEdit, enc encoding) ([]ext.BufferEditParams, map[string][]textEdit, error) {
	byURI, resourceOps, err := w.fileEdits()
	if err != nil {
		return nil, nil, err
	}
	if resourceOps {
		return nil, nil, errors.New("ファイルの作成、名前の変更、削除を含む")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []ext.BufferEditParams
	unopened := map[string][]textEdit{}
	for _, uri := range slices.Sorted(maps.Keys(byURI)) {
		path, ok := uriToPath(uri)
		if !ok {
			return nil, nil, fmt.Errorf("ファイルでない URI を含む: %s", uri)
		}
		d := c.docByPath(path)
		if d == nil {
			unopened[path] = append(unopened[path], byURI[uri]...)
			continue
		}
		cur := d.current()
		t := formattingTransaction(cur.lines, d.finalNewline, cur.version, byURI[uri], enc)
		if t != nil {
			out = append(out, ext.BufferEditParams{Buffer: d.buffer, Transaction: *t})
		}
	}
	return out, unopened, nil
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/ext"
)

// hostTimeout は、1 回のツールの呼び出しで本体の応答を待つ上限である（仮）。
// 本体は、拡張機能への問い合わせの結果を待つ間、拡張機能からのリクエストを取っておく（基本設計 5.3）。
// マクロの再生中の待ちは上限（ReplayTimeout。既定で 10 秒）まで続きうるので、それより長くする。
const hostTimeout = 30 * time.Second

// editorAPI は、ツールが使う本体への問い合わせと、拡張機能が持つバッファの複製の読み出しである。
// 実物は hostEditor（ext/server の Server と複製の一覧）で、テストでは偽物に差し替える。
// どのメソッドも、ext/server のメッセージループの外の goroutine から呼ぶ。
type editorAPI interface {
	EditorState(ctx context.Context) (ext.EditorStateResult, error)
	ListDiagnostics(ctx context.Context, p ext.DiagnosticListParams) (ext.DiagnosticListResult, error)
	Edit(ctx context.Context, p ext.BufferEditParams) (ext.BufferEditResult, error)
	OpenEditor(ctx context.Context, p ext.EditorOpenParams) (ext.EditorOpenResult, error)
	SaveBuffer(ctx context.Context, p ext.BufferSaveParams) (ext.BufferSaveResult, error)
	// ReadBuffer は、バッファ id の複製が version 以降になるまで待ち、その時点の行の列とバージョン番号を返す。
	// 本体がそのバッファを拡張機能へ送っていない（不正な UTF-8 を含む）場合は errNotSent を返す。
	ReadBuffer(ctx context.Context, id ext.BufferID, version ext.Version) (snapshot, error)
}

// snapshot は、バッファの複製のある時点の内容である。
type snapshot struct {
	Version ext.Version
	// Lines は、改行を含まない行の列である。空のバッファでも 1 行ある（ext/server の Buffer）。
	Lines []string
}

// errNotSent は、本体がバッファを拡張機能へ送っていないことを表す（基本設計 5.3）。
var errNotSent = errors.New("the buffer contains invalid UTF-8, so the editor does not send it to extensions")

// toolError は、ツールの実行の誤りである。MCP の結果の isError を true にして、本文に Error() を書く。
// 言語モデルが引数を直して再度試せるようにするためである（MCP の Tools の「Error Handling」）。
type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func toolErrorf(format string, args ...any) error {
	return &toolError{msg: fmt.Sprintf(format, args...)}
}

// tool は、1 つのツールの定義と実装である。
type tool struct {
	name        string
	title       string
	description string
	// schema は、引数の JSON Schema（2020-12）である。
	schema   string
	readOnly bool
	// run は、引数を読んで結果を返す。返した値は JSON にして text の内容にする。
	// *toolError 以外の誤りも、結果の isError で返す（本体の応答の誤りなど、言語モデルが知るべきもの）。
	run func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error)
}

// toolbox は、ツールの一覧である。並びは tools/list で返す順で、変わらない。
type toolbox struct {
	ed    editorAPI
	tools []tool
}

func newToolbox(ed editorAPI) *toolbox {
	return &toolbox{ed: ed, tools: []tool{
		listBuffersTool, getEditorStateTool, readBufferTool, getDiagnosticsTool, editBufferTool, openFileTool, saveBufferTool,
	}}
}

// list は、tools/list の tools を返す。
func (tb *toolbox) list() []map[string]any {
	out := make([]map[string]any, 0, len(tb.tools))
	for _, t := range tb.tools {
		d := map[string]any{
			"name":        t.name,
			"title":       t.title,
			"description": t.description,
			"inputSchema": json.RawMessage(t.schema),
		}
		if t.readOnly {
			d["annotations"] = map[string]any{"readOnlyHint": true}
		} else {
			d["annotations"] = map[string]any{"readOnlyHint": false, "destructiveHint": false}
		}
		out = append(out, d)
	}
	return out
}

// call は、tools/call を処理する。名前の無いツールは JSON-RPC の誤り（-32602）で、
// ツールの実行の誤りは isError が true の結果で返す（MCP の Tools の「Error Handling」）。
func (tb *toolbox) call(ctx context.Context, name string, args json.RawMessage) (map[string]any, *rpcError) {
	i := slices.IndexFunc(tb.tools, func(t tool) bool { return t.name == name })
	if i < 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "Unknown tool: " + name}
	}
	if len(bytes.TrimSpace(args)) == 0 || string(bytes.TrimSpace(args)) == "null" {
		args = json.RawMessage("{}")
	}
	ctx, cancel := context.WithTimeout(ctx, hostTimeout)
	defer cancel()
	v, err := tb.tools[i].run(ctx, tb.ed, args)
	if err != nil {
		return map[string]any{"content": []any{textContent(err.Error())}, "isError": true}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "marshal tool result: " + err.Error()}
	}
	return map[string]any{"content": []any{textContent(string(b))}, "isError": false}, nil
}

func textContent(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

// decodeArgs は、ツールの引数を v へ読む。知らない項目は誤りにする（綴りの誤りを黙って無視しないため）。
func decodeArgs(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return toolErrorf("invalid arguments: %v", err)
	}
	return nil
}

// 位置の変換（基本設計 6.2）。
// ツールの位置は、行を 1 から、列を行の先頭から 1 から数えたバイト数で表す（Vim の line() と col()）。
// 拡張機能 API の位置（ext.Position）は、どちらも 0 から数える（基本設計 4.1）。

// mcpPosition は、ツールの引数と結果の位置である。
type mcpPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// mcpRange は、ツールの引数と結果の範囲である。End は範囲の直後を指す（End の文字は含まない）。
type mcpRange struct {
	Start mcpPosition `json:"start"`
	End   mcpPosition `json:"end"`
}

func fromExtPos(p ext.Position) mcpPosition { return mcpPosition{Line: p.Line + 1, Column: p.Byte + 1} }

func fromExtRange(r ext.Range) mcpRange {
	return mcpRange{Start: fromExtPos(r.Start), End: fromExtPos(r.End)}
}

// toExtPos は、ツールの位置を拡張機能 API の位置にする。行と列が 1 未満なら誤りを返す。
// 行と列がバッファの中にあるかは、本体が確かめる。
func toExtPos(p mcpPosition) (ext.Position, error) {
	if p.Line < 1 || p.Column < 1 {
		return ext.Position{}, toolErrorf("invalid position line %d column %d: line and column start at 1", p.Line, p.Column)
	}
	return ext.Position{Line: p.Line - 1, Byte: p.Column - 1}, nil
}

func toExtRange(r mcpRange) (ext.Range, error) {
	s, err := toExtPos(r.Start)
	if err != nil {
		return ext.Range{}, err
	}
	e, err := toExtPos(r.End)
	if err != nil {
		return ext.Range{}, err
	}
	return ext.Range{Start: s, End: e}, nil
}

// bufferRef は、バッファを番号かパスで指す引数である。
type bufferRef struct {
	Buffer *ext.BufferID `json:"buffer,omitempty"`
	Path   string        `json:"path,omitempty"`
}

const bufferRefSchema = `"buffer": {"type": "integer", "minimum": 1, "description": "Buffer number from list_buffers. Give either buffer or path."},
    "path": {"type": "string", "description": "Absolute path of an open buffer. Give either buffer or path."}`

// resolve は、ref が指す開いているバッファを、editor/state の一覧から探す。
// ref が空のときは、required なら誤りを、そうでなければ nil を返す。
//
// パスは filepath.Clean で整えてから比べる。本体は利用者が開いたパスを絶対パスにして渡し、シンボリックリンクは解決しない（基本設計 6.7）。
// 言語モデルは `./` や `..` を含むパスを作りやすいので、文字列の一致だけでは開いているバッファを見つけられないことがある。
func (ref bufferRef) resolve(st *ext.EditorStateResult, required bool) (*ext.BufferInfo, error) {
	switch {
	case ref.Buffer != nil && ref.Path != "":
		return nil, toolErrorf("give either buffer or path, not both")
	case ref.Buffer == nil && ref.Path == "":
		if required {
			return nil, toolErrorf("buffer or path is required")
		}
		return nil, nil
	case ref.Path != "" && !filepath.IsAbs(ref.Path):
		return nil, toolErrorf("path %q is not an absolute path", ref.Path)
	}
	want := ""
	if ref.Path != "" {
		want = filepath.Clean(ref.Path)
	}
	for i := range st.Buffers {
		b := &st.Buffers[i]
		if (ref.Buffer != nil && b.Buffer == *ref.Buffer) || (want != "" && b.Path != "" && filepath.Clean(b.Path) == want) {
			return b, nil
		}
	}
	if ref.Buffer != nil {
		return nil, toolErrorf("buffer %d is not open; call list_buffers", *ref.Buffer)
	}
	return nil, toolErrorf("no open buffer has path %q; call open_file to open it", ref.Path)
}

// pathOf は、バッファの番号からパスを引く表を作る。
func pathOf(st *ext.EditorStateResult) map[ext.BufferID]string {
	m := make(map[ext.BufferID]string, len(st.Buffers))
	for _, b := range st.Buffers {
		m[b.Buffer] = b.Path
	}
	return m
}

const emptySchema = `{"type": "object", "additionalProperties": false}`

// list_buffers: 開いているバッファの一覧（editor/state の buffers）。
//
// req: FR-32
var listBuffersTool = tool{
	name:        "list_buffers",
	title:       "List buffers",
	description: "List the buffers open in the editor: number, absolute path, file type, version, whether it has unsaved changes, and whether the cursor is in it.",
	schema:      emptySchema,
	readOnly:    true,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		if err := decodeArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		type item struct {
			Buffer   ext.BufferID `json:"buffer"`
			Path     string       `json:"path,omitempty"`
			FileType string       `json:"fileType,omitempty"`
			Version  ext.Version  `json:"version"`
			Modified bool         `json:"modified"`
			Current  bool         `json:"current"`
		}
		out := struct {
			Buffers []item `json:"buffers"`
		}{Buffers: []item{}}
		for _, b := range st.Buffers {
			out.Buffers = append(out.Buffers, item{
				Buffer: b.Buffer, Path: b.Path, FileType: b.FileType, Version: b.Version, Modified: b.Modified, Current: b.Buffer == st.Buffer,
			})
		}
		return out, nil
	},
}

// get_editor_state: カーソルのあるバッファ、カーソル位置、モード、選択範囲（editor/state）。
//
// req: FR-32
var getEditorStateTool = tool{
	name:  "get_editor_state",
	title: "Get editor state",
	description: "Get the buffer the cursor is in, the cursor position, the mode " +
		"(normal, insert, visual, visualLine, visualBlock, commandLine), and the visual selection. " +
		"In visualBlock mode the selection has one range per line. A range's end points just after its last byte.",
	schema:   emptySchema,
	readOnly: true,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		if err := decodeArgs(args, &struct{}{}); err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		out := struct {
			Buffer     ext.BufferID `json:"buffer"`
			Path       string       `json:"path,omitempty"`
			Version    ext.Version  `json:"version"`
			Cursor     mcpPosition  `json:"cursor"`
			Mode       ext.Mode     `json:"mode"`
			Selections []mcpRange   `json:"selections"`
		}{Buffer: st.Buffer, Cursor: fromExtPos(st.Cursor), Mode: st.Mode, Selections: []mcpRange{}}
		for _, b := range st.Buffers {
			if b.Buffer == st.Buffer {
				out.Path, out.Version = b.Path, b.Version
			}
		}
		for _, r := range st.Selections {
			out.Selections = append(out.Selections, fromExtRange(r))
		}
		return out, nil
	},
}

// read_buffer: バッファの全文か行の範囲と、そのバージョン番号（拡張機能が持つ複製）。
//
// req: FR-32
var readBufferTool = tool{
	name:  "read_buffer",
	title: "Read buffer",
	description: "Read the text of an open buffer, as the editor holds it including unsaved changes, with its version. " +
		"Pass the version to edit_buffer. startLine and endLine (1-based, inclusive) limit the lines; " +
		"by default the whole buffer is returned. The text has no trailing newline after the last line.",
	schema: `{
  "type": "object",
  "properties": {
    ` + bufferRefSchema + `,
    "startLine": {"type": "integer", "minimum": 1, "description": "First line to read (1-based). Default 1."},
    "endLine": {"type": "integer", "minimum": 1, "description": "Last line to read (1-based, inclusive). Default the last line."}
  },
  "additionalProperties": false
}`,
	readOnly: true,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		var a struct {
			bufferRef
			StartLine *int `json:"startLine,omitempty"`
			EndLine   *int `json:"endLine,omitempty"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		info, err := a.resolve(&st, true)
		if err != nil {
			return nil, err
		}
		snap, err := ed.ReadBuffer(ctx, info.Buffer, info.Version)
		if err != nil {
			return nil, err
		}
		n := len(snap.Lines)
		start, end := 1, n
		if a.StartLine != nil {
			start = *a.StartLine
		}
		if a.EndLine != nil {
			end = min(*a.EndLine, n)
		}
		if start < 1 || start > n {
			return nil, toolErrorf("startLine %d is out of range: the buffer has %d lines", start, n)
		}
		if end < start {
			return nil, toolErrorf("endLine %d is before startLine %d", end, start)
		}
		return struct {
			Buffer    ext.BufferID `json:"buffer"`
			Path      string       `json:"path,omitempty"`
			Version   ext.Version  `json:"version"`
			LineCount int          `json:"lineCount"`
			StartLine int          `json:"startLine"`
			EndLine   int          `json:"endLine"`
			Text      string       `json:"text"`
		}{
			Buffer: info.Buffer, Path: info.Path, Version: snap.Version, LineCount: n,
			StartLine: start, EndLine: end, Text: strings.Join(snap.Lines[start-1:end], "\n"),
		}, nil
	},
}

// get_diagnostics: 本体が受け取り済みの診断（diagnostic/list）。
//
// req: FR-32
var getDiagnosticsTool = tool{
	name:  "get_diagnostics",
	title: "Get diagnostics",
	description: "Get the diagnostics (errors, warnings) that extensions such as the language server client reported. " +
		"Without buffer or path, returns those of all open buffers. Positions refer to the returned version of each buffer.",
	schema: `{
  "type": "object",
  "properties": {
    ` + bufferRefSchema + `
  },
  "additionalProperties": false
}`,
	readOnly: true,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		var a bufferRef
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		info, err := a.resolve(&st, false)
		if err != nil {
			return nil, err
		}
		var p ext.DiagnosticListParams
		if info != nil {
			p.Buffer = &info.Buffer
		}
		res, err := ed.ListDiagnostics(ctx, p)
		if err != nil {
			return nil, err
		}
		type diag struct {
			Range    mcpRange               `json:"range"`
			Severity ext.DiagnosticSeverity `json:"severity"`
			Message  string                 `json:"message"`
			Source   string                 `json:"source,omitempty"`
			Code     string                 `json:"code,omitempty"`
		}
		type entry struct {
			Buffer      ext.BufferID `json:"buffer"`
			Path        string       `json:"path,omitempty"`
			Extension   string       `json:"extension"`
			Version     ext.Version  `json:"version"`
			Diagnostics []diag       `json:"diagnostics"`
		}
		paths := pathOf(&st)
		out := struct {
			Entries []entry `json:"entries"`
		}{Entries: []entry{}}
		for _, e := range res.Entries {
			en := entry{Buffer: e.Buffer, Path: paths[e.Buffer], Extension: e.Extension, Version: e.Version, Diagnostics: []diag{}}
			for _, d := range e.Diagnostics {
				en.Diagnostics = append(en.Diagnostics, diag{
					Range: fromExtRange(d.Range), Severity: d.Severity, Message: d.Message, Source: d.Source, Code: d.Code,
				})
			}
			out.Entries = append(out.Entries, en)
		}
		return out, nil
	},
}

// mcpEdit は、edit_buffer の 1 つの置き換えである。
type mcpEdit struct {
	Range mcpRange `json:"range"`
	Text  string   `json:"text"`
}

// toTransaction は、edit_buffer の置き換えの列を、本体のトランザクションにする。
//
// edit_buffer の位置は、すべて version の時点のテキストで数える（言語モデルは read_buffer の結果から位置を数えるので）。
// 本体のトランザクションは逐次の数え方（各編集の位置は、直前の編集までを適用した後のテキストで数える。基本設計 4.2）なので、
// 範囲の重ならない置き換えを、後ろのものから順に並べて送る。後ろから適用すれば、前の置き換えの位置は変わらない。
//
// 開始の位置で並べ、ある置き換えの開始が、その前の置き換えの終わりより前にあれば、重なるとして誤りにする。
// 同じ位置への挿入（範囲が空の置き換え）が並ぶ場合は、引数の順に並んだテキストになる。
func toTransaction(version ext.Version, edits []mcpEdit) (ext.Transaction, error) {
	if len(edits) == 0 {
		return ext.Transaction{}, toolErrorf("edits is empty")
	}
	type indexed struct {
		i int
		e ext.Edit
	}
	conv := make([]indexed, 0, len(edits))
	for i, e := range edits {
		r, err := toExtRange(e.Range)
		if err != nil {
			return ext.Transaction{}, toolErrorf("edits[%d]: %v", i, err)
		}
		if before(r.End, r.Start) {
			return ext.Transaction{}, toolErrorf("edits[%d]: range end is before its start", i)
		}
		conv = append(conv, indexed{i: i, e: ext.Edit{Range: r, Text: e.Text}})
	}
	slices.SortStableFunc(conv, func(a, b indexed) int { return comparePos(a.e.Range.Start, b.e.Range.Start) })
	for k := 1; k < len(conv); k++ {
		if before(conv[k].e.Range.Start, conv[k-1].e.Range.End) {
			return ext.Transaction{}, toolErrorf("edits[%d] and edits[%d] overlap; ranges must not overlap", conv[k-1].i, conv[k].i)
		}
	}
	tx := ext.Transaction{BaseVersion: version, Edits: make([]ext.Edit, 0, len(conv))}
	for k := len(conv) - 1; k >= 0; k-- {
		tx.Edits = append(tx.Edits, conv[k].e)
	}
	return tx, nil
}

// checkEdits は、edit_buffer の置き換えの位置が、lines（version の時点のテキスト）の中の文字の境界を指すことを確かめる。
// 誤りの本文は、ツールの数え方（1 始まりの行と列）と引数の edits[i] で書く。
// 本体も同じ検査をするが、その誤りの本文は 0 始まりの位置で、トランザクションの並べ替えた後の番号を指すので、言語モデルが読み違えうる。
// 文字の境界の判定は ext/server の Buffer と同じく、正しい UTF-8 の多バイト文字の途中だけを境界でないとする。
func checkEdits(lines []string, edits []mcpEdit) error {
	for i, e := range edits {
		for _, p := range []mcpPosition{e.Range.Start, e.Range.End} {
			if err := checkPosition(lines, p); err != nil {
				return toolErrorf("edits[%d]: %v", i, err)
			}
		}
	}
	return nil
}

// checkPosition は、1 始まりの位置 p が lines の中の文字の境界（行末を含む）を指すことを確かめる。
func checkPosition(lines []string, p mcpPosition) error {
	if p.Line < 1 || p.Line > len(lines) {
		return fmt.Errorf("line %d is out of range: the buffer has lines 1 to %d", p.Line, len(lines))
	}
	line := lines[p.Line-1]
	if p.Column < 1 || p.Column > len(line)+1 {
		return fmt.Errorf("column %d of line %d is out of range: the line has %d bytes, so the column is 1 to %d (%d is the end of the line)",
			p.Column, p.Line, len(line), len(line)+1, len(line)+1)
	}
	b := p.Column - 1
	for j := max(0, b-(utf8.UTFMax-1)); j < b; j++ {
		if _, size := utf8.DecodeRuneInString(line[j:]); size > 1 && j+size > b {
			return fmt.Errorf("column %d of line %d is inside a multi-byte character that starts at column %d; columns count bytes", p.Column, p.Line, j+1)
		}
	}
	return nil
}

func comparePos(p, q ext.Position) int {
	if p.Line != q.Line {
		return p.Line - q.Line
	}
	return p.Byte - q.Byte
}

func before(p, q ext.Position) bool { return comparePos(p, q) < 0 }

// edit_buffer: 範囲の置き換えの列を、read_buffer で得たバージョン番号と一緒に本体へ送る（buffer/edit）。
// 本体は、バージョン番号が現在と一致したときだけ適用する（FR-15）。
//
// req: FR-32
var editBufferTool = tool{
	name:  "edit_buffer",
	title: "Edit buffer",
	description: "Replace ranges of an open buffer in the editor (not the file on disk; call save_buffer to write it). " +
		"version must be the version from read_buffer; if the buffer has changed since, nothing is applied and you must read it again. " +
		"All ranges refer to the text at that version and must not overlap. " +
		"A range's start and end are 1-based line and byte column; end points just after the last replaced byte " +
		"(to insert, give start equal to end; the end of a line is column = its length in bytes + 1). " +
		"The edits are applied as one undoable change.",
	schema: `{
  "type": "object",
  "properties": {
    ` + bufferRefSchema + `,
    "version": {"type": "integer", "minimum": 0, "description": "Version from read_buffer."},
    "edits": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "properties": {
          "range": {
            "type": "object",
            "properties": {
              "start": {"$ref": "#/$defs/position"},
              "end": {"$ref": "#/$defs/position"}
            },
            "required": ["start", "end"],
            "additionalProperties": false
          },
          "text": {"type": "string", "description": "Replacement text. May contain newlines."}
        },
        "required": ["range", "text"],
        "additionalProperties": false
      }
    }
  },
  "required": ["version", "edits"],
  "additionalProperties": false,
  "$defs": {
    "position": {
      "type": "object",
      "properties": {
        "line": {"type": "integer", "minimum": 1},
        "column": {"type": "integer", "minimum": 1, "description": "Byte offset in the line, from 1."}
      },
      "required": ["line", "column"],
      "additionalProperties": false
    }
  }
}`,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		var a struct {
			bufferRef
			Version *ext.Version `json:"version"`
			Edits   []mcpEdit    `json:"edits"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		if a.Version == nil {
			return nil, toolErrorf("version is required; call read_buffer to get it")
		}
		tx, err := toTransaction(*a.Version, a.Edits)
		if err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		info, err := a.resolve(&st, true)
		if err != nil {
			return nil, err
		}
		// 位置の誤りを、ツールの数え方の本文で返すために、送る前に複製で確かめる。
		// バッファが既に別の版なら、本体がバージョン番号の不一致で拒むので確かめない。
		// 複製を読めない（不正な UTF-8 で送られていない）ときも、本体の判断に任せる。
		if info.Version == *a.Version {
			if snap, rerr := ed.ReadBuffer(ctx, info.Buffer, info.Version); rerr == nil && snap.Version == *a.Version {
				if cerr := checkEdits(snap.Lines, a.Edits); cerr != nil {
					return nil, cerr
				}
			}
		}
		res, err := ed.Edit(ctx, ext.BufferEditParams{Buffer: info.Buffer, Transaction: tx})
		if err != nil {
			// 本体の誤りの本文は、0 始まりの位置と、並べ替えた後のトランザクションの番号で書かれている。
			return nil, fmt.Errorf("%w (positions in this message are 0-based and refer to the edits sorted from the end of the buffer)", err)
		}
		if !res.Applied && res.Version != *a.Version {
			return nil, toolErrorf("not applied: version %d does not match the current version %d of buffer %d; "+
				"call read_buffer again and retry with the new version", *a.Version, res.Version, info.Buffer)
		}
		// 適用したが内容が変わらなかった場合も、本体は Applied を false にする（internal/editor の editRequest）。
		return struct {
			Buffer  ext.BufferID `json:"buffer"`
			Applied bool         `json:"applied"`
			Version ext.Version  `json:"version"`
		}{Buffer: info.Buffer, Applied: res.Applied, Version: res.Version}, nil
	},
}

// open_file: ファイルを開き、指定があればカーソルを動かす（editor/open）。
//
// req: FR-32
var openFileTool = tool{
	name:  "open_file",
	title: "Open file",
	description: "Open a file in the editor (or switch to it if already open) and optionally move the cursor. " +
		"Returns the buffer number and version. A file that does not exist yet opens as an empty buffer with that path.",
	schema: `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "Absolute path of the file."},
    "line": {"type": "integer", "minimum": 1, "description": "Line to move the cursor to (1-based)."},
    "column": {"type": "integer", "minimum": 1, "description": "Byte column to move the cursor to (1-based). Default 1. Requires line."},
    "version": {"type": "integer", "minimum": 0, "description": "Version the line and column refer to. If given, the cursor moves only when the buffer is still at this version."},
    "focus": {"type": "boolean", "description": "Make it the buffer the cursor is in. Default true."}
  },
  "required": ["path"],
  "additionalProperties": false
}`,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		var a struct {
			Path    string       `json:"path"`
			Line    *int         `json:"line,omitempty"`
			Column  *int         `json:"column,omitempty"`
			Version *ext.Version `json:"version,omitempty"`
			Focus   *bool        `json:"focus,omitempty"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(a.Path) {
			return nil, toolErrorf("path %q is not an absolute path", a.Path)
		}
		// 本体は既に開いているファイルをパスで探すので、`./` や `..` を除いてから渡す（bufferRef.resolve と同じ）。
		p := ext.EditorOpenParams{Path: filepath.Clean(a.Path), Version: a.Version, Focus: a.Focus == nil || *a.Focus}
		switch {
		case a.Line != nil:
			col := 1
			if a.Column != nil {
				col = *a.Column
			}
			pos, err := toExtPos(mcpPosition{Line: *a.Line, Column: col})
			if err != nil {
				return nil, err
			}
			p.Position = &pos
		case a.Column != nil:
			return nil, toolErrorf("column requires line")
		}
		res, err := ed.OpenEditor(ctx, p)
		if err != nil {
			return nil, err
		}
		return struct {
			Buffer  ext.BufferID `json:"buffer"`
			Version ext.Version  `json:"version"`
			Moved   bool         `json:"moved"`
		}{Buffer: res.Buffer, Version: res.Version, Moved: res.Moved}, nil
	},
}

// save_buffer: バッファをファイルへ書き出す（buffer/save）。利用者の保存と同じ保存の流れを通る（基本設計 6.7）。
//
// req: FR-32
var saveBufferTool = tool{
	name:  "save_buffer",
	title: "Save buffer",
	description: "Write an open buffer to its file, the same way as the user's :w (including formatting on save by other extensions). " +
		"If version is given, the buffer is written only when it is still at that version.",
	schema: `{
  "type": "object",
  "properties": {
    ` + bufferRefSchema + `,
    "version": {"type": "integer", "minimum": 0, "description": "Write only if the buffer is at this version."}
  },
  "additionalProperties": false
}`,
	run: func(ctx context.Context, ed editorAPI, args json.RawMessage) (any, error) {
		var a struct {
			bufferRef
			Version *ext.Version `json:"version,omitempty"`
		}
		if err := decodeArgs(args, &a); err != nil {
			return nil, err
		}
		st, err := ed.EditorState(ctx)
		if err != nil {
			return nil, err
		}
		info, err := a.resolve(&st, true)
		if err != nil {
			return nil, err
		}
		res, err := ed.SaveBuffer(ctx, ext.BufferSaveParams{Buffer: info.Buffer, Version: a.Version})
		if err != nil {
			return nil, err
		}
		if !res.Saved {
			if a.Version != nil && *a.Version != res.Version {
				return nil, toolErrorf("not saved: version %d does not match the current version %d of buffer %d", *a.Version, res.Version, info.Buffer)
			}
			return nil, toolErrorf("failed to save buffer %d; the editor shows the reason to the user", info.Buffer)
		}
		return struct {
			Buffer  ext.BufferID `json:"buffer"`
			Path    string       `json:"path,omitempty"`
			Saved   bool         `json:"saved"`
			Version ext.Version  `json:"version"`
		}{Buffer: info.Buffer, Path: info.Path, Saved: true, Version: res.Version}, nil
	},
}

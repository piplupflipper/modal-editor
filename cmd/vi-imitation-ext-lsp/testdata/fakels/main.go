// Command fakels は、vi-imitation-ext-lsp のテストで言語サーバーの代わりに使う、LSP を話すプログラムである。
//
// 受け取った文書を、自分の位置の数え方（-encoding）で contentChanges を適用して持ち、次のとおりに答える。
// 位置の変換は、拡張機能の実装とは別に、unicode/utf16 で数えて書く。
//
//   - 文書を開くか変えるたびに、"ERR" の出現ごとにエラー、"WARN" の出現ごとに code 42 の警告の診断を、その版の番号で送る
//   - textDocument/definition: 開いている文書と -defpath のファイルの中の "TARGET" の出現の位置をすべて返す
//   - textDocument/rename: 開いている文書と、開いていなければ -defpath のファイルの中の "TARGET" を、すべて新しい名前にする
//   - textDocument/hover: "char=<位置の文字>" を Markdown で返す
//   - textDocument/formatting: "fmtme" の出現ごとに、同じ開始の位置の 2 つの編集（"<" の挿入と "FMTME" への置き換え）を、後ろの出現から順に返す。
//     -fmt-whole なら、同じ結果の全文で、文書の全体（文書の末尾まで。末尾が改行なら、その後ろの空の行の先頭）を置き換える 1 つの編集を返す
//   - didChange の後の文書が "CRASH" を含めば、終了コード 3 で終わる
//   - -publish-on-close なら、didClose を受け取ったときに、閉じる前の文書の診断を送ってから文書を捨て、"published on close" の行を書く
//     （閉じた文書の診断を後から送る言語サーバーの代役）
//
// 受け取ったメッセージと、各時点の文書の全文は、-log のファイルへ JSON の行で書き足す。
// 起動したときに、自分と親（拡張機能）のプロセス ID を "start" の行で書く。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

var (
	encodingFlag = flag.String("encoding", "utf-16", "選ぶ位置の数え方。utf-16、utf-8、または none（positionEncoding を省く）")
	syncFlag     = flag.Int("sync", 2, "TextDocumentSyncKind")
	logFlag      = flag.String("log", "", "受け取ったメッセージを書き足すファイル")
	defPath      = flag.String("defpath", "", "定義の候補に足すファイル")
	renamePaths  = flag.String("renamepaths", "", "名前の変更で、開いていなくても数えるファイル（, で区切る）")
	links        = flag.Bool("links", false, "定義を LocationLink の配列で返す")
	fmtWhole     = flag.Bool("fmt-whole", false, "整形を全体の置き換えで返す")
	fmtDelay     = flag.Duration("fmt-delay", 0, "整形の応答を遅らせる時間")
	initDelay    = flag.Duration("init-delay", 0, "initialize の応答を遅らせる時間")
	hangShutdown = flag.Bool("hang-on-shutdown", false, "shutdown に応答せず、exit でも終わらない（止まった言語サーバーの代役）")
	publishClose = flag.Bool("publish-on-close", false, "didClose を受け取ったときに、閉じる前の文書の診断を送る")
)

type pos struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type rng struct {
	Start pos `json:"start"`
	End   pos `json:"end"`
}

var (
	mu      sync.Mutex
	docs    = map[string]string{}
	vers    = map[string]int64{}
	logFile *os.File
	conn    *rpc.Conn
)

func logEntry(kind string, v any) {
	mu.Lock()
	defer mu.Unlock()
	if logFile == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{"kind": kind, "value": v})
	_, _ = logFile.Write(append(data, '\n'))
}

// units は、r を数え方で数えた単位の数である。
func units(r rune) int {
	switch *encodingFlag {
	case "utf-8":
		return utf8.RuneLen(r)
	default:
		return len(utf16.Encode([]rune{r}))
	}
}

// offset は、text の中の LSP の位置 p をバイト位置にする。
func offset(text string, p pos) int {
	lines := strings.SplitAfter(text, "\n")
	off := 0
	for i := 0; i < p.Line && i < len(lines); i++ {
		off += len(lines[i])
	}
	if p.Line >= len(lines) {
		return len(text)
	}
	line := strings.TrimSuffix(lines[p.Line], "\n")
	n := 0
	for i, r := range line {
		if n >= p.Character {
			return off + i
		}
		n += units(r)
	}
	return off + len(line)
}

// position は、text のバイト位置 off を LSP の位置にする。
func position(text string, off int) pos {
	before := text[:off]
	line := strings.Count(before, "\n")
	start := strings.LastIndex(before, "\n") + 1
	n := 0
	for _, r := range before[start:] {
		n += units(r)
	}
	return pos{Line: line, Character: n}
}

func occurrences(text, word string) []int {
	var offs []int
	for i := 0; ; {
		j := strings.Index(text[i:], word)
		if j < 0 {
			return offs
		}
		offs = append(offs, i+j)
		i += j + len(word)
	}
}

func publish(uri string) {
	mu.Lock()
	text, v := docs[uri], vers[uri]
	mu.Unlock()
	diags := []map[string]any{}
	for _, o := range occurrences(text, "ERR") {
		diags = append(diags, map[string]any{
			"range": rng{position(text, o), position(text, o+3)}, "severity": 1, "message": "err", "source": "fakels",
		})
	}
	for _, o := range occurrences(text, "WARN") {
		diags = append(diags, map[string]any{
			"range": rng{position(text, o), position(text, o+4)}, "severity": 2, "message": "warn", "code": 42,
		})
	}
	_ = conn.Notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "version": v, "diagnostics": diags})
}

func main() {
	flag.Parse()
	if *logFlag != "" {
		f, err := os.OpenFile(*logFlag, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		logFile = f
	}
	logEntry("start", map[string]int{"pid": os.Getpid(), "ppid": os.Getppid()})
	conn = rpc.NewConn(os.Stdin, os.Stdout, rpc.HandlerFuncs{
		Request:      request,
		Notification: notification,
	}, rpc.WithCancelMethod("$/cancelRequest"))
	<-conn.Done()
	logEntry("closed", conn.Err().Error())
	os.Exit(4)
}

func request(ctx context.Context, req *rpc.Request, reply rpc.Replier) {
	logEntry(req.Method, req.Params)
	switch req.Method {
	case "initialize":
		caps := map[string]any{
			"textDocumentSync":           map[string]any{"openClose": true, "change": *syncFlag, "save": map[string]any{"includeText": true}},
			"definitionProvider":         true,
			"hoverProvider":              map[string]any{},
			"documentFormattingProvider": true,
			"referencesProvider":         true,
			"renameProvider":             true,
		}
		if *encodingFlag != "none" {
			caps["positionEncoding"] = *encodingFlag
		}
		go func() {
			time.Sleep(*initDelay)
			_ = reply(map[string]any{"capabilities": caps}, nil)
		}()
	case "shutdown":
		if *hangShutdown {
			return
		}
		_ = reply(nil, nil)
	case "textDocument/rename":
		// 開いている文書の TARGET をすべて newName に変える。
		var p struct {
			NewName string `json:"newName"`
		}
		_ = json.Unmarshal(req.Params, &p)
		changes := map[string][]map[string]any{}
		mu.Lock()
		add := func(u, text string) {
			for _, o := range occurrences(text, "TARGET") {
				changes[u] = append(changes[u], map[string]any{"range": rng{position(text, o), position(text, o+6)}, "newText": p.NewName})
			}
		}
		for u, text := range docs {
			add(u, text)
		}
		mu.Unlock()
		// -defpath と -renamepaths のファイルは、開いていなければ、ファイルの中身で数える（開いていない文書に及ぶ名前の変更。FR-109）。
		extra := []string{*defPath}
		if *renamePaths != "" {
			extra = append(extra, strings.Split(*renamePaths, ",")...)
		}
		for _, path := range extra {
			if u := "file://" + path; path != "" && changes[u] == nil {
				if data, err := os.ReadFile(path); err == nil {
					add(u, string(data))
				}
			}
		}
		if len(changes) == 0 {
			_ = reply(nil, nil)
			return
		}
		_ = reply(map[string]any{"changes": changes}, nil)
	case "textDocument/definition", "textDocument/references":
		// 参照の一覧も、定義の候補と同じく、開いている文書（と -defpath のファイル）の TARGET の位置を返す。
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(req.Params, &p)
		mu.Lock()
		uris := make([]string, 0, len(docs))
		for u := range docs {
			uris = append(uris, u)
		}
		sort.Strings(uris)
		var locs []any
		add := func(uri, text string) {
			for _, o := range occurrences(text, "TARGET") {
				r := rng{position(text, o), position(text, o+6)}
				if *links {
					locs = append(locs, map[string]any{"targetUri": uri, "targetRange": r, "targetSelectionRange": r})
				} else {
					locs = append(locs, map[string]any{"uri": uri, "range": r})
				}
			}
		}
		for _, u := range uris {
			add(u, docs[u])
		}
		mu.Unlock()
		if *defPath != "" {
			data, err := os.ReadFile(*defPath)
			if err == nil {
				add("file://"+*defPath, string(data))
			}
		}
		if len(locs) == 0 {
			_ = reply(nil, nil)
			return
		}
		_ = reply(locs, nil)
	case "textDocument/hover":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Position     pos                  `json:"position"`
		}
		_ = json.Unmarshal(req.Params, &p)
		mu.Lock()
		text := docs[p.TextDocument.URI]
		mu.Unlock()
		o := offset(text, p.Position)
		r, _ := utf8.DecodeRuneInString(text[o:])
		_ = reply(map[string]any{"contents": map[string]any{"kind": "markdown", "value": fmt.Sprintf("char=%c", r)}}, nil)
	case "textDocument/formatting":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(req.Params, &p)
		mu.Lock()
		text := docs[p.TextDocument.URI]
		mu.Unlock()
		go func() {
			if *fmtDelay > 0 {
				select {
				case <-time.After(*fmtDelay):
				case <-ctx.Done():
					logEntry("formatting cancelled", nil)
					_ = reply(nil, &rpc.Error{Code: -32800, Message: "cancelled"})
					return
				}
			}
			_ = reply(formatEdits(text), nil)
		}()
	default:
		_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: req.Method})
	}
}

func formatEdits(text string) []map[string]any {
	offs := occurrences(text, "fmtme")
	if *fmtWhole {
		formatted := strings.ReplaceAll(text, "fmtme", "<FMTME")
		return []map[string]any{{"range": rng{pos{0, 0}, position(text, len(text))}, "newText": formatted}}
	}
	edits := []map[string]any{}
	for i := len(offs) - 1; i >= 0; i-- {
		o := offs[i]
		s, e := position(text, o), position(text, o+5)
		edits = append(edits,
			map[string]any{"range": rng{s, s}, "newText": "<"},
			map[string]any{"range": rng{s, e}, "newText": "FMTME"},
		)
	}
	return edits
}

func notification(_ context.Context, n *rpc.Notification) {
	logEntry(n.Method, n.Params)
	switch n.Method {
	case "initialized":
		go func() {
			var res json.RawMessage
			err := conn.Call(context.Background(), "workspace/configuration",
				map[string]any{"items": []map[string]any{{"section": "fake"}, {"section": "other"}}}, &res)
			if err != nil {
				logEntry("configuration error", err.Error())
				return
			}
			logEntry("configuration result", res)
		}()
	case "exit":
		if *hangShutdown {
			return
		}
		os.Exit(0)
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int64  `json:"version"`
				Text    string `json:"text"`
			} `json:"textDocument"`
		}
		_ = json.Unmarshal(n.Params, &p)
		mu.Lock()
		docs[p.TextDocument.URI] = p.TextDocument.Text
		vers[p.TextDocument.URI] = p.TextDocument.Version
		mu.Unlock()
		logEntry("text", map[string]any{"uri": p.TextDocument.URI, "version": p.TextDocument.Version, "text": p.TextDocument.Text})
		publish(p.TextDocument.URI)
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int64  `json:"version"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Range *rng   `json:"range"`
				Text  string `json:"text"`
			} `json:"contentChanges"`
		}
		_ = json.Unmarshal(n.Params, &p)
		uri := p.TextDocument.URI
		mu.Lock()
		text := docs[uri]
		for _, c := range p.ContentChanges {
			if c.Range == nil {
				text = c.Text
				continue
			}
			s, e := offset(text, c.Range.Start), offset(text, c.Range.End)
			text = text[:s] + c.Text + text[e:]
		}
		docs[uri] = text
		vers[uri] = p.TextDocument.Version
		mu.Unlock()
		logEntry("text", map[string]any{"uri": uri, "version": p.TextDocument.Version, "text": text})
		if strings.Contains(text, "CRASH") {
			os.Exit(3)
		}
		publish(uri)
	case "textDocument/didClose":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		_ = json.Unmarshal(n.Params, &p)
		if *publishClose {
			publish(p.TextDocument.URI)
			logEntry("published on close", p.TextDocument.URI)
		}
		mu.Lock()
		delete(docs, p.TextDocument.URI)
		mu.Unlock()
	}
}

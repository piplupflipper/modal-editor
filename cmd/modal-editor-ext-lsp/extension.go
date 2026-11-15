package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// コマンドの ID（基本設計 6.3。名前は仮）。宣言ファイル manifest.json の contributes.commands と一致させる。
const (
	cmdDefinition = "lsp.definition"
	cmdHover      = "lsp.hover"
	cmdFormat     = "lsp.format"
)

// stopTimeout は、拡張機能が終わるときに、言語サーバーの shutdown と exit を待つ時間の上限である。
const stopTimeout = 2 * time.Second

// extension は、LSP の拡張機能の全体である。
// ファイルの種類ごとの client を持ち、ext/server のフックとハンドラーを client へ振り分ける。
type extension struct {
	srv *server.Server
	log *log.Logger
	// ctx は、拡張機能の寿命の ctx である。本体の応答を待たない処理（sync が false のコマンドの続き）に使う。
	ctx context.Context
	// requestTimeout は、sync が false のコマンドが言語サーバーの応答を待つ時間の上限である。
	requestTimeout time.Duration

	// mu は clients を守る。clients は initialize で作り、その後は変えない。
	mu      sync.Mutex
	clients map[string]*client
	// tune は、テストが client の待ち時間などを変えるための関数である。nil なら何もしない。
	tune func(*client)

	// byBuffer は、バッファの番号から、そのバッファを同期している client への対応である。ループだけが触る。
	byBuffer map[ext.BufferID]*client
}

// newExtension は、ハンドラーを登録した extension を作る。ctx は、拡張機能の寿命の ctx である。
func newExtension(ctx context.Context) *extension {
	x := &extension{
		srv:            server.New(),
		log:            log.New(os.Stderr, "modal-editor-ext-lsp: ", log.LstdFlags),
		ctx:            ctx,
		requestTimeout: defaultRequestTimeout,
		byBuffer:       make(map[ext.BufferID]*client),
	}
	x.srv.ErrorLog = x.log
	x.srv.OnInitialize(x.initialize)
	x.srv.OnBufferOpen(x.bufferOpen)
	x.srv.OnBufferChange(func(_ context.Context, e *server.BufferChangeEvent) {
		if c := x.byBuffer[e.Buffer.ID()]; c != nil {
			c.change(e)
		}
	})
	x.srv.OnBufferClose(func(_ context.Context, e *server.BufferCloseEvent) {
		if c := x.byBuffer[e.Buffer]; c != nil {
			c.close(e.Buffer)
			delete(x.byBuffer, e.Buffer)
		}
	})
	x.srv.OnBufferDidSave(func(_ context.Context, e *server.BufferDidSaveEvent) {
		if c := x.byBuffer[e.Params.Buffer]; c != nil {
			c.didSave(e)
		}
	})
	x.srv.CommandAsync(cmdDefinition, x.definition)
	x.srv.CommandAsync(cmdHover, x.hover)
	x.srv.CommandAsync(cmdFormat, x.format)
	x.srv.BufferWillSaveAsync(x.willSave)
	return x
}

// stop は、すべての言語サーバーを止める。Serve が戻った後に呼ぶ。
func (x *extension) stop() {
	x.mu.Lock()
	cs := make([]*client, 0, len(x.clients))
	for _, c := range x.clients {
		cs = append(cs, c)
	}
	x.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Go(func() { c.stop(ctx) })
	}
	wg.Wait()
}

func (x *extension) logf(format string, args ...any) { x.log.Printf(format, args...) }

// showMessage は、本体へ message/show を送る。送れなければログに書く。
func (x *extension) showMessage(level ext.MessageLevel, text string) {
	if err := x.srv.ShowMessage(level, text); err != nil {
		x.logf("message/show: %v", err)
	}
}

// setDiagnostics は、本体へ diagnostic/set を送る。送れなければログに書く。
func (x *extension) setDiagnostics(p ext.DiagnosticSetParams) {
	if err := x.srv.SetDiagnostics(p); err != nil {
		x.logf("diagnostic/set: %v", err)
	}
}

// initialize は、initialize の settings を読み、ファイルの種類ごとの client を作る。
// 設定に誤りがあれば initialize に失敗を応答する。
func (x *extension) initialize(_ context.Context, e *server.InitializeEvent) error {
	cfg, err := parseSettings(e.Settings)
	if err != nil {
		return err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.clients = make(map[string]*client, len(cfg.servers))
	for ft, sc := range cfg.servers {
		c := newClient(x, ft, sc, cfg.maxRestarts)
		if x.tune != nil {
			x.tune(c)
		}
		x.clients[ft] = c
	}
	return nil
}

// bufferOpen は、設定に言語サーバーのあるファイルの種類のバッファを、その client で同期する。
// ファイルに対応しないバッファ（パスが空）は、言語サーバーへ URI で示せないので同期しない。
func (x *extension) bufferOpen(_ context.Context, e *server.BufferOpenEvent) {
	b := e.Buffer
	x.mu.Lock()
	c := x.clients[b.FileType()]
	x.mu.Unlock()
	if b.Path() == "" {
		c = nil
	}
	old := x.byBuffer[b.ID()]
	if old != nil && old != c {
		old.close(b.ID())
		delete(x.byBuffer, b.ID())
		old = nil
	}
	if c == nil {
		return
	}
	x.byBuffer[b.ID()] = c
	c.open(b, e.Reopen && old != nil)
}

// sendFor は、コマンドの対象のバッファについて、言語サーバーへリクエストを送る。ループから呼ぶ。
// 送れなければ、利用者に理由を表示して誤りを返す。
func (x *extension) sendFor(
	p *ext.CommandExecuteParams, what, method string,
	has func(*serverCapabilities) bool,
	params func(uri string, ls lines, enc encoding) any,
) (*request, error) {
	c := x.byBuffer[p.Buffer]
	if c == nil {
		x.showMessage(ext.MessageWarning, "このバッファには言語サーバーが無い")
		return nil, errors.New("no language server for the buffer")
	}
	r, err := c.send(p.Buffer, p.Version, method, has, params)
	switch {
	case errors.Is(err, errNotReady):
		x.showMessage(ext.MessageWarning, fmt.Sprintf("言語サーバー %s が稼働していない", c.name()))
	case errors.Is(err, errUnsupported):
		x.showMessage(ext.MessageWarning, fmt.Sprintf("言語サーバー %s は%sに対応していない", c.name(), what))
	case err != nil:
		x.logf("%s: %v", method, err)
	}
	return r, err
}

// positionParams は、カーソル位置 cursor の textDocument/definition と textDocument/hover の params を作る関数を返す。
func positionParams(cursor ext.Position) func(uri string, ls lines, enc encoding) any {
	return func(uri string, ls lines, enc encoding) any {
		return textDocumentPositionParams{TextDocument: textDocumentIdentifier{URI: uri}, Position: ls.toLSP(cursor, enc)}
	}
}

// background は、本体の応答を待たない処理の ctx を返す。
func (x *extension) background() (context.Context, context.CancelFunc) {
	return context.WithTimeout(x.ctx, x.requestTimeout)
}

// definition は、コマンド lsp.definition を処理する（基本設計 6.3）。
//
// カーソル位置で textDocument/definition を送る。
// sync が false なら、受け付けたことだけを ok で応答し、結果は後から送る。
// 候補が 1 つなら editor/open でその位置へ移動し、複数なら choice/ask で利用者に選ばせる。候補が無ければ message/show で知らせる。
// sync が true（マクロとドットリピートの再生中）なら、結果がそろってから、最初の候補への移動を応答の open で返す。
// 本体は応答を待つ間、この拡張機能からの choice/ask を処理できないので、再生中は選ばせない。候補が無ければ ok を false にする。
//
// req: FR-30
func (x *extension) definition(ctx context.Context, _ *server.Buffer, p *ext.CommandExecuteParams, reply func(ext.CommandExecuteResult, error)) {
	r, err := x.sendFor(p, "定義へ移動", lspDefinition,
		func(c *serverCapabilities) bool { return provides(c.DefinitionProvider) }, positionParams(p.Cursor))
	if err != nil {
		reply(ext.CommandExecuteResult{OK: false}, nil)
		return
	}
	if p.Sync {
		go func() {
			targets, err := x.definitionTargets(ctx, r)
			if err != nil || len(targets) == 0 {
				if err != nil {
					x.logf("%s: %v", lspDefinition, err)
				}
				reply(ext.CommandExecuteResult{OK: false}, nil)
				return
			}
			open := targets[0].open
			reply(ext.CommandExecuteResult{OK: true, Open: &open}, nil)
		}()
		return
	}
	reply(ext.CommandExecuteResult{OK: true}, nil)
	go func() {
		ctx, cancel := x.background()
		defer cancel()
		targets, err := x.definitionTargets(ctx, r)
		if err != nil {
			x.logf("%s: %v", lspDefinition, err)
			x.showMessage(ext.MessageError, fmt.Sprintf("定義へ移動できない: %v", err))
			return
		}
		var open ext.EditorOpenParams
		switch len(targets) {
		case 0:
			x.showMessage(ext.MessageInfo, "定義が見つからない")
			return
		case 1:
			open = targets[0].open
		default:
			choices := make([]ext.Choice, len(targets))
			for i, t := range targets {
				choices[i] = ext.Choice{ID: fmt.Sprint(i), Label: t.label}
			}
			// 利用者が選ぶのを待つので、requestTimeout の ctx ではなく拡張機能の寿命の ctx で待つ。
			res, err := x.srv.AskChoice(x.ctx, ext.ChoiceAskParams{Title: "定義の候補", Choices: choices})
			if err != nil || res.Cancelled {
				if err != nil {
					x.logf("choice/ask: %v", err)
				}
				return
			}
			i := slices.IndexFunc(choices, func(c ext.Choice) bool { return c.ID == res.Selected })
			if i < 0 {
				return
			}
			open = targets[i].open
		}
		ctx, cancel = x.background()
		defer cancel()
		if _, err := x.srv.OpenEditor(ctx, open); err != nil {
			x.logf("editor/open: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("定義へ移動できない: %v", err))
		}
	}()
}

// target は、定義へ移動の 1 つの候補である。
type target struct {
	open  ext.EditorOpenParams
	label string
}

// definitionTargets は、textDocument/definition の応答を待ち、候補を本体の位置にして返す。
func (x *extension) definitionTargets(ctx context.Context, r *request) ([]target, error) {
	var raw json.RawMessage
	if err := r.wait(ctx, &raw); err != nil {
		return nil, err
	}
	locs, err := parseLocations(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid definition result: %w", err)
	}
	var targets []target
	for _, l := range locs {
		uri, pos, ok := l.target()
		if !ok {
			continue
		}
		path, ok := uriToPath(uri)
		if !ok {
			continue
		}
		t := r.c.resolve(r, path, pos)
		if slices.ContainsFunc(targets, func(u target) bool { return u.label == t.label }) {
			continue
		}
		targets = append(targets, t)
	}
	return targets, nil
}

// resolve は、言語サーバーの示したファイル path の位置 pos を、editor/open の params にする。
//
//   - 問い合わせた文書なら、問い合わせた版の行で変換し、バッファの番号とその版の番号を指定する。
//     本体は、その間にバッファが変わっていれば、開くだけでカーソルを動かさない
//   - この client が同期している他の文書なら、その現在の版の行で変換し、バッファの番号と版の番号を指定する
//   - 開いていないファイルなら、ファイルを読んで変換し、パスを指定する。読めなければ位置を指定せずに開く
func (c *client) resolve(r *request, path string, pos lspPosition) target {
	t := target{open: ext.EditorOpenParams{Focus: true}}
	var ls lines
	var version ext.Version
	if path == r.path {
		ls, version = r.lines, r.version
		id := r.buffer
		t.open.Buffer = &id
	} else {
		c.mu.Lock()
		if d := c.docByPath(path); d != nil {
			cur := d.current()
			ls, version = cur.lines, cur.version
			id := d.buffer
			t.open.Buffer = &id
		}
		c.mu.Unlock()
	}
	if t.open.Buffer != nil {
		t.open.Version = &version
	} else {
		t.open.Path = path
		if data, err := os.ReadFile(path); err == nil {
			ls = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
	}
	t.label = fmt.Sprintf("%s:%d", path, pos.Line+1)
	if ls != nil {
		at, _ := ls.fromLSP(pos, r.enc)
		t.open.Position = &at
		t.label = fmt.Sprintf("%s:%d:%d", path, at.Line+1, at.Byte+1)
	}
	return t
}

// hover は、コマンド lsp.hover を処理する（基本設計 6.3）。
// カーソル位置で textDocument/hover を送り、受け付けたことだけを ok で応答する。
// 結果の本文は popup/show で、問い合わせた版の番号とカーソル位置と一緒に送る。
// 表示は再生の結果を変えないので、sync が true でも後から送る（基本設計 6 章の冒頭）。
//
// req: FR-30
func (x *extension) hover(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams, reply func(ext.CommandExecuteResult, error)) {
	r, err := x.sendFor(p, "ホバー", lspHover,
		func(c *serverCapabilities) bool { return provides(c.HoverProvider) }, positionParams(p.Cursor))
	if err != nil {
		reply(ext.CommandExecuteResult{OK: false}, nil)
		return
	}
	reply(ext.CommandExecuteResult{OK: true}, nil)
	cursor := p.Cursor
	go func() {
		ctx, cancel := x.background()
		defer cancel()
		var res *hoverResult
		if err := r.wait(ctx, &res); err != nil {
			x.logf("%s: %v", lspHover, err)
			x.showMessage(ext.MessageError, fmt.Sprintf("ホバーの情報を得られない: %v", err))
			return
		}
		var text, format string
		if res != nil {
			text, format = hoverText(res.Contents)
		}
		if strings.TrimSpace(text) == "" {
			x.showMessage(ext.MessageInfo, "ホバーの情報が無い")
			return
		}
		popup := ext.PopupShowParams{Buffer: r.buffer, Version: r.version, Position: cursor, Text: text, Format: ext.PopupFormat(format)}
		if err := x.srv.ShowPopup(popup); err != nil {
			x.logf("popup/show: %v", err)
		}
	}()
}

// formattingParams は、textDocument/formatting の params を作る関数を返す。
func formattingParams(cfg *serverConfig) func(uri string, ls lines, enc encoding) any {
	return func(uri string, _ lines, _ encoding) any {
		return documentFormattingParams{
			TextDocument: textDocumentIdentifier{URI: uri},
			Options:      formattingOptions{TabSize: cfg.tabSize(), InsertSpaces: cfg.InsertSpaces},
		}
	}
}

// formatTransaction は、textDocument/formatting の応答を待ち、トランザクションにする。整形の変更が無ければ nil を返す。
func formatTransaction(ctx context.Context, r *request) (*ext.Transaction, error) {
	var edits []textEdit
	if err := r.wait(ctx, &edits); err != nil {
		return nil, err
	}
	return formattingTransaction(r.lines, r.version, edits, r.enc), nil
}

// hasFormatting は、言語サーバーが textDocument/formatting に対応するかどうかを返す。
func hasFormatting(c *serverCapabilities) bool { return provides(c.DocumentFormattingProvider) }

// format は、コマンド lsp.format を処理する（基本設計 6.3）。
//
// textDocument/formatting を送る。sync が false なら、受け付けたことだけを ok で応答し、結果を buffer/edit で送る。
// その間に利用者が編集していれば、本体は適用しない（FR-15）ので、message/show でそのことを知らせる。
// sync が true なら、結果がそろってから、編集を応答の transaction で返す。
//
// req: FR-30
func (x *extension) format(ctx context.Context, _ *server.Buffer, p *ext.CommandExecuteParams, reply func(ext.CommandExecuteResult, error)) {
	c := x.byBuffer[p.Buffer]
	if c == nil {
		x.showMessage(ext.MessageWarning, "このバッファには言語サーバーが無い")
		reply(ext.CommandExecuteResult{OK: false}, nil)
		return
	}
	r, err := x.sendFor(p, "整形", lspFormatting, hasFormatting, formattingParams(c.cfg))
	if err != nil {
		reply(ext.CommandExecuteResult{OK: false}, nil)
		return
	}
	if p.Sync {
		go func() {
			t, err := formatTransaction(ctx, r)
			if err != nil {
				x.logf("%s: %v", lspFormatting, err)
				reply(ext.CommandExecuteResult{OK: false}, nil)
				return
			}
			reply(ext.CommandExecuteResult{OK: true, Transaction: t}, nil)
		}()
		return
	}
	reply(ext.CommandExecuteResult{OK: true}, nil)
	go func() {
		ctx, cancel := x.background()
		defer cancel()
		t, err := formatTransaction(ctx, r)
		if err != nil {
			x.logf("%s: %v", lspFormatting, err)
			x.showMessage(ext.MessageError, fmt.Sprintf("整形できない: %v", err))
			return
		}
		if t == nil {
			return
		}
		res, err := x.srv.Edit(ctx, ext.BufferEditParams{Buffer: r.buffer, Transaction: *t})
		switch {
		case err != nil:
			x.logf("buffer/edit: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("整形を適用できない: %v", err))
		case !res.Applied:
			x.showMessage(ext.MessageWarning, "整形の間にバッファが変わったので、整形を適用しなかった")
		}
	}()
}

// willSave は、保存の直前の buffer/willSave に、textDocument/formatting の結果のトランザクションを答える（基本設計 6.3、6.7）。
// 保存の前の整形を設定で止めているか、言語サーバーが稼働していないか、整形に対応しなければ、編集無しを答える。
// 稼働していない（起動中か再起動を待っている）ときは、整形しなかったことを message/show で知らせる。
// 本体が期限を超えて $/cancel を送ったら、言語サーバーへ $/cancelRequest を送り、取り消しを応答する。
//
// req: FR-30
func (x *extension) willSave(ctx context.Context, _ *server.Buffer, p *ext.BufferWillSaveParams, reply func(ext.BufferWillSaveResult, error)) {
	c := x.byBuffer[p.Buffer]
	if c == nil || !c.cfg.formatOnSave() {
		reply(ext.BufferWillSaveResult{}, nil)
		return
	}
	r, err := c.send(p.Buffer, p.Version, lspFormatting, hasFormatting, formattingParams(c.cfg))
	if err != nil {
		if errors.Is(err, errNotReady) {
			x.showMessage(ext.MessageWarning, fmt.Sprintf("言語サーバー %s が稼働していないので、保存の前の整形をしなかった", c.name()))
		}
		reply(ext.BufferWillSaveResult{}, nil)
		return
	}
	go func() {
		t, err := formatTransaction(ctx, r)
		switch {
		case ctx.Err() != nil:
			reply(ext.BufferWillSaveResult{}, ctx.Err())
		case err != nil:
			x.logf("%s before saving: %v", lspFormatting, err)
			reply(ext.BufferWillSaveResult{}, nil)
		default:
			reply(ext.BufferWillSaveResult{Transaction: t}, nil)
		}
	}()
}

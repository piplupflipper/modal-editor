package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

// indentSync は、開いているバッファごとに、整形ツールの設定ファイルから読んだインデントを本体へ渡す（indent.go。#349。FR-193）。
//
// いつ読むかは次のとおりである。
//
//   - buffer/open を受けたとき（開いたとき、別名で保存したとき、:set filetype= でファイルの種類が変わったとき。本体が送り直す）
//   - buffer/didSave を受けたとき。保存したファイルが整形ツールの設定ファイル（configNames）なら開いているすべてのバッファを、
//     そうでなければそのバッファだけを読み直す。エディタの外で設定ファイルを変えたときも、そのバッファを保存すれば効く
//
// 設定ファイルの変更を見張る仕組みは持たない（本体にも拡張機能 API にも無い）。
// 本体へのリクエストはハンドラーの上で送れない（ext/server の Edit の doc）ので、1 つの goroutine（loop）がまとめて送る。
// 1 つの goroutine で送るので、同じバッファの値が届く順は、読んだ順と同じになる。
// 前に送った値と同じなら送らない。読めない設定ファイルは、本体の設定を変えずに、同じファイルの同じ誤りを 1 回だけ知らせる
// （直した後に同じ誤りでまた壊しても、もう知らせない。誤りの文言が変われば知らせる）。
type indentSync struct {
	cfg func() *config
	// setOptions は、本体へ option/set を送る（Server.SetOptions）。テストは差し替える。
	setOptions func(ctx context.Context, p ext.OptionSetParams) error
	// warn は、利用者へ警告を出す（message/show）。テストは差し替える。
	warn func(text string)
	// logf は、ログを書く。
	logf func(format string, args ...any)

	mu      sync.Mutex
	bufs    map[ext.BufferID]bufferInfo
	pending map[ext.BufferID]bool
	// retyped は、前の buffer/open からファイルの種類が変わったバッファである。読み直したときに、値が同じでも送る。
	// 最後に送った種類と比べるだけでは、A から B へ変わってすぐ A に戻ったときの 2 つの buffer/open を 1 回の読み直しにまとめると、
	// 送らずに済ませてしまい、本体が :set の値を隠さない（PR #352 の再レビューの必須 1）。
	retyped map[ext.BufferID]bool
	wake    chan struct{}
	started bool

	// sent は、バッファごとに最後に送った値である。loop だけが触る。
	sent map[ext.BufferID]sentIndent
	// reported は、設定ファイルのパスごとに最後に知らせた誤りの文言である。loop だけが触る。
	reported map[string]string
}

// bufferInfo は、インデントを読むのに要るバッファの属性である。
type bufferInfo struct {
	path, fileType string
}

// configNames は、保存したときに開いているすべてのバッファのインデントを読み直す、整形ツールの設定ファイルの名前である。
var configNames = slices.Concat(biomeFiles, []string{".ruff.toml", "ruff.toml", "pyproject.toml"}, prettierFiles)

func newIndentSync(cfg func() *config, setOptions func(context.Context, ext.OptionSetParams) error, warn func(string), logf func(string, ...any)) *indentSync {
	return &indentSync{
		cfg: cfg, setOptions: setOptions, warn: warn, logf: logf,
		bufs: map[ext.BufferID]bufferInfo{}, pending: map[ext.BufferID]bool{}, retyped: map[ext.BufferID]bool{}, wake: make(chan struct{}, 1),
		sent: map[ext.BufferID]sentIndent{}, reported: map[string]string{},
	}
}

// register は、x のサーバーのフックに、バッファの開閉と保存の知らせを登録する。
func (s *indentSync) register(ctx context.Context, srv *server.Server) {
	srv.OnBufferOpen(func(_ context.Context, e *server.BufferOpenEvent) {
		s.opened(e.Buffer.ID(), bufferInfo{path: e.Buffer.Path(), fileType: e.Buffer.FileType()})
		s.start(ctx)
	})
	srv.OnBufferClose(func(_ context.Context, e *server.BufferCloseEvent) { s.closed(e.Buffer) })
	srv.OnBufferDidSave(func(_ context.Context, e *server.BufferDidSaveEvent) { s.saved(e.Params.Buffer) })
}

// start は、送る goroutine を 1 回だけ起こす。
func (s *indentSync) start(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return
	}
	s.started = true
	go s.loop(ctx)
}

// opened は、バッファ id を開いた（buffer/open を受けた）ことを記録し、読み直しを頼む。
// 前の buffer/open からファイルの種類が変わっていれば、読み直したときに値が同じでも送る印を付ける。
func (s *indentSync) opened(id ext.BufferID, info bufferInfo) {
	s.mu.Lock()
	if prev, ok := s.bufs[id]; ok && prev.fileType != info.fileType {
		s.retyped[id] = true
	}
	s.bufs[id] = info
	s.pending[id] = true
	s.mu.Unlock()
	s.signal()
}

// closed は、バッファ id を閉じたことを記録する。
func (s *indentSync) closed(id ext.BufferID) {
	s.mu.Lock()
	delete(s.bufs, id)
	delete(s.pending, id)
	delete(s.retyped, id)
	s.mu.Unlock()
}

// saved は、バッファ id を保存したことを受けて、読み直しを頼む。保存したファイルが整形ツールの設定ファイルなら、すべてのバッファを読み直す。
func (s *indentSync) saved(id ext.BufferID) {
	s.mu.Lock()
	info, ok := s.bufs[id]
	if ok && slices.Contains(configNames, filepath.Base(info.path)) {
		for b := range s.bufs {
			s.pending[b] = true
		}
	} else if ok {
		s.pending[id] = true
	}
	s.mu.Unlock()
	s.signal()
}

func (s *indentSync) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// loop は、読み直しを頼まれたバッファを順に処理する。ctx が終わるまで戻らない。
func (s *indentSync) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
		for s.step(ctx) {
		}
	}
}

// step は、読み直しを頼まれたバッファを 1 つ処理する。頼まれたバッファが無ければ false を返す。
func (s *indentSync) step(ctx context.Context) bool {
	s.mu.Lock()
	var id ext.BufferID
	found := false
	for b := range s.pending {
		if !found || b < id {
			id, found = b, true
		}
	}
	if !found {
		s.mu.Unlock()
		return false
	}
	delete(s.pending, id)
	info, open := s.bufs[id]
	retyped := s.retyped[id]
	delete(s.retyped, id)
	s.mu.Unlock()
	if open {
		s.apply(ctx, id, info, retyped)
	} else {
		delete(s.sent, id)
	}
	return true
}

// apply は、バッファ id のインデントを読み、前に送った値かファイルの種類と違えば option/set で送る（FR-193）。
// 送るときは、値を決めたファイルの種類を FileType に付ける（本体は、ファイルの種類が変わった後の最初の値で :set の値を隠す）。
// ファイルの種類だけが変わったときも送り直すのは、本体に新しい種類の値として :set の値を隠させるためである。
// retyped（前の buffer/open からファイルの種類が変わった）なら、値も種類も前に送ったものと同じでも送る（A→B→A の 2 つの
// buffer/open を 1 回の読み直しにまとめたとき。本体は、種類が変わった後の最初の値を待っている）。
//
// 読めない設定ファイルがあれば、知らせる（同じファイルの同じ誤りは 1 回だけ）。
// 前に送った値をその設定ファイルから読んだなら、送らずに前の値を保つ（本体の設定を変えない）。
// そうでなければ（ファイルの種類が変わって別の整形ツールの設定を読むようになったなど）、前の値を null で取り下げる。
// 別の整形ツールの値が残らないようにするためである（PR #352 のレビューの推奨 7）。
//
// req: FR-193
func (s *indentSync) apply(ctx context.Context, id ext.BufferID, info bufferInfo, retyped bool) {
	if info.path == "" {
		return
	}
	ic, sources, err := s.cfg().projectIndent(info.path, info.fileType)
	if err != nil {
		var ce *configError
		if !errors.As(err, &ce) {
			s.logf("indent: %v", err)
			return
		}
		if s.reported[ce.path] != ce.Error() {
			s.reported[ce.path] = ce.Error()
			s.warn(ce.Error())
		}
		if prev, ok := s.sent[id]; ok && slices.Contains(prev.sources, ce.path) {
			return
		}
		ic, sources = indentConfig{}, nil
	}
	opts := ic.options()
	b, _ := json.Marshal(opts) // 誤りにならない
	prev, had := s.sent[id]
	if had && !retyped && string(b) == prev.options && prev.fileType == info.fileType || !had && ic == (indentConfig{}) {
		if had {
			prev.sources = sources
			s.sent[id] = prev
		}
		return
	}
	if err := s.setOptions(ctx, ext.OptionSetParams{Buffer: id, Options: opts, FileType: info.fileType}); err != nil {
		s.logf("option/set: %v", err)
		return
	}
	s.sent[id] = sentIndent{options: string(b), fileType: info.fileType, sources: sources}
}

// sentIndent は、バッファへ最後に送った値である。
type sentIndent struct {
	// options は、送った options の JSON である。
	options string
	// fileType は、値を決めたファイルの種類である。
	fileType string
	// sources は、値を読んだ設定ファイルのパスである。
	sources []string
}

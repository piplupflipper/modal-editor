package main

import (
	"context"
	"log"
	"os"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/ext/server"
)

// highlighter は、バッファごとの構文木を持ち、buffer/open と buffer/change のたびに色を付ける範囲の一覧を本体へ送る。
//
// フックは Server がメッセージを処理する 1 つの goroutine から呼ぶ（server.Server の doc）ので、
// highlighter のフィールドはロックなしで触る。
type highlighter struct {
	langs map[string]*language // ファイルの種類から言語へ
	docs  map[ext.BufferID]*document
	// send は、decoration/set を送る関数である。server が Server の SetDecorations を入れる。
	send func(ext.DecorationSetParams) error
	log  *log.Logger
}

// newHighlighter は、languages のすべての言語のパーサーとクエリを用意する。
// クエリを解釈できないなど、用意できない言語があれば誤りを返す。
func newHighlighter() (*highlighter, error) {
	h := &highlighter{
		langs: map[string]*language{},
		docs:  map[ext.BufferID]*document{},
		log:   log.New(os.Stderr, "modal-editor-ext-treesitter: ", log.LstdFlags),
	}
	for _, spec := range languages {
		l, err := newLanguage(spec)
		if err != nil {
			h.close()
			return nil, err
		}
		h.langs[spec.fileType] = l
	}
	return h, nil
}

// close は、すべての構文木とパーサーとクエリの C のメモリを解放する。
func (h *highlighter) close() {
	for _, d := range h.docs {
		d.close()
	}
	clear(h.docs)
	for _, l := range h.langs {
		l.close()
	}
	clear(h.langs)
}

// server は、フックを登録した Server を返す。
func (h *highlighter) server() *server.Server {
	s := server.New()
	s.ErrorLog = h.log
	s.OnBufferOpen(func(_ context.Context, e *server.BufferOpenEvent) { h.open(e.Buffer) })
	s.OnBufferChange(func(_ context.Context, e *server.BufferChangeEvent) { h.change(e) })
	s.OnBufferClose(func(_ context.Context, e *server.BufferCloseEvent) { h.drop(e.Buffer) })
	h.send = s.SetDecorations
	return s
}

// open は、buffer/open で受け取った全文を解析し、一覧を送る。
// 文法を持たない種類のファイルは解析しない。
// 送り直された全文（buffer/resync の応答）でファイルの種類が文法を持たないものに変わっていたら、構文木を捨て、空の一覧を送って色を消す。
//
// req: FR-35
func (h *highlighter) open(b *server.Buffer) {
	lang, ok := h.langs[b.FileType()]
	if !ok {
		if h.drop(b.ID()) {
			h.publish(b.ID(), b.Version(), []ext.Decoration{})
		}
		return
	}
	if d, ok := h.docs[b.ID()]; ok && d.lang == lang {
		d.reset(b.Text())
	} else {
		h.drop(b.ID())
		h.docs[b.ID()] = newDocument(lang, b.Text())
	}
	h.publish(b.ID(), b.Version(), h.docs[b.ID()].decorations())
}

// change は、buffer/change の編集を構文木に適用して増分で解析し直し、一覧を送る。
// 編集を適用できなければ（手元の全文と本体の内容がずれていれば）、Server が持つ複製の全文から解析し直す。
//
// req: FR-35
func (h *highlighter) change(e *server.BufferChangeEvent) {
	d, ok := h.docs[e.Buffer.ID()]
	if !ok {
		return
	}
	if err := d.applyEdits(e.Edits); err != nil {
		h.log.Printf("buffer %d: %v; reparsing the whole text", e.Buffer.ID(), err)
		d.reset(e.Buffer.Text())
	}
	h.publish(e.Buffer.ID(), e.ToVersion, d.decorations())
}

// drop は、バッファの構文木を捨てる。持っていたかどうかを返す。
func (h *highlighter) drop(id ext.BufferID) bool {
	d, ok := h.docs[id]
	if !ok {
		return false
	}
	d.close()
	delete(h.docs, id)
	return true
}

// publish は、decoration/set を送る。送れなければログに書く。
func (h *highlighter) publish(id ext.BufferID, version ext.Version, decs []ext.Decoration) {
	if err := h.send(ext.DecorationSetParams{Buffer: id, Version: version, Decorations: decs}); err != nil {
		h.log.Printf("buffer %d: decoration/set: %v", id, err)
	}
}

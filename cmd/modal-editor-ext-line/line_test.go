package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

const testTimeout = 5 * time.Second

// connect は、newServer の Server を io.Pipe の上で動かし、本体の役の接続を返す。
// initialize を済ませ、text を buffer/open で送った状態（バッファ 1、バージョン 1）にする。
func connect(t *testing.T, text string) *rpc.Conn {
	t.Helper()
	hostR, extW := io.Pipe()
	extR, hostW := io.Pipe()
	served := make(chan error, 1)
	go func() { served <- newServer().Serve(context.Background(), extR, extW) }()
	conn := rpc.NewConn(hostR, hostW, nil)
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve = %v, want nil", err)
			}
		case <-time.After(testTimeout):
			t.Error("Serve did not return after the host closed")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var init ext.InitializeResult
	if err := conn.Call(ctx, ext.MethodInitialize, ext.InitializeParams{APIVersion: ext.APIVersion}, &init); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Version: 1, Text: text}); err != nil {
		t.Fatalf("buffer/open: %v", err)
	}
	return conn
}

func resolve(t *testing.T, conn *rpc.Conn, p ext.TextObjectResolveParams) (ext.TextObjectResolveResult, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	var res ext.TextObjectResolveResult
	err := conn.Call(ctx, ext.MethodTextObjectResolve, p, &res)
	return res, err
}

func pos(line, b int) ext.Position { return ext.Position{Line: line, Byte: b} }

// TestResolve は、別の goroutine で動かした拡張機能へ JSON-RPC で textObject/resolve を問い合わせ、範囲を確かめる。
//
// req: FR-11
func TestResolve(t *testing.T) {
	// 行 0: 先頭と末尾に空白とタブ
	// 行 1: 空の行
	// 行 2: 空白だけの行
	// 行 3: 多バイト文字と全角空白（全角空白は空白として扱わない）
	// 行 4: 空白の無い行
	const text = "  \tfoo bar \t\n\n   \n  あい　\nx"
	tests := []struct {
		name   string
		id     string
		cursor ext.Position
		count  int
		ok     bool
		want   ext.Range
	}{
		{"inner trims both ends", idInner, pos(0, 5), 0, true, ext.Range{Start: pos(0, 3), End: pos(0, 10)}},
		{"inner count 1 equals no count", idInner, pos(0, 0), 1, true, ext.Range{Start: pos(0, 3), End: pos(0, 10)}},
		{"inner on empty line fails", idInner, pos(1, 0), 0, false, ext.Range{}},
		{"inner on blank line fails", idInner, pos(2, 1), 0, false, ext.Range{}},
		{"inner over blank lines fails", idInner, pos(1, 0), 2, false, ext.Range{}},
		{"inner keeps ideographic space", idInner, pos(3, 2), 0, true, ext.Range{Start: pos(3, 2), End: pos(3, 11)}},
		{"inner with count spans lines", idInner, pos(0, 0), 4, true, ext.Range{Start: pos(0, 3), End: pos(3, 11)}},
		{"inner count skips leading blank lines", idInner, pos(1, 0), 3, true, ext.Range{Start: pos(3, 2), End: pos(3, 11)}},
		{"inner on last line", idInner, pos(4, 0), 0, true, ext.Range{Start: pos(4, 0), End: pos(4, 1)}},
		{"inner count beyond last line fails", idInner, pos(3, 0), 3, false, ext.Range{}},
		{"around whole line", idAround, pos(0, 5), 0, true, ext.Range{Start: pos(0, 0), End: pos(0, 12)}},
		{"around empty line is empty", idAround, pos(1, 0), 0, true, ext.Range{Start: pos(1, 0), End: pos(1, 0)}},
		{"around with count", idAround, pos(2, 0), 3, true, ext.Range{Start: pos(2, 0), End: pos(4, 1)}},
		{"around count beyond last line fails", idAround, pos(4, 0), 2, false, ext.Range{}},
	}
	conn := connect(t, text)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(t, conn, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: tt.id, Cursor: tt.cursor, Count: tt.count})
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.OK != tt.ok {
				t.Fatalf("OK = %v, want %v (result %+v)", got.OK, tt.ok, got)
			}
			if got.Version != 1 {
				t.Errorf("Version = %d, want 1", got.Version)
			}
			if tt.ok && got.Range != tt.want {
				t.Errorf("Range = %+v, want %+v", got.Range, tt.want)
			}
			if got.Linewise {
				t.Error("Linewise = true, want false")
			}
		})
	}
}

// TestResolveAfterChange は、buffer/change で更新した複製の上で範囲を求めることを確かめる。
func TestResolveAfterChange(t *testing.T) {
	conn := connect(t, "abc\n  def")
	// 行 0 の先頭に空白を 2 つ挿入する。
	err := conn.Notify(ext.MethodBufferChange, ext.BufferChangeParams{
		Buffer: 1, FromVersion: 1, ToVersion: 2,
		Edits: []ext.Edit{{Range: ext.Range{Start: pos(0, 0), End: pos(0, 0)}, Text: "  "}},
	})
	if err != nil {
		t.Fatalf("buffer/change: %v", err)
	}
	got, err := resolve(t, conn, ext.TextObjectResolveParams{Buffer: 1, Version: 2, ID: idInner, Cursor: pos(0, 0)})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := ext.Range{Start: pos(0, 2), End: pos(0, 5)}
	if !got.OK || got.Version != 2 || got.Range != want {
		t.Errorf("result = %+v, want OK, version 2, range %+v", got, want)
	}
}

// TestResolveInvalidCursor は、バッファの外のカーソルに JSON-RPC のエラーで応答することを確かめる。
func TestResolveInvalidCursor(t *testing.T) {
	conn := connect(t, "abc")
	for _, line := range []int{-1, 1} {
		_, err := resolve(t, conn, ext.TextObjectResolveParams{Buffer: 1, Version: 1, ID: idInner, Cursor: pos(line, 0)})
		var rerr *rpc.Error
		if !errors.As(err, &rerr) {
			t.Errorf("cursor line %d: err = %v, want *rpc.Error", line, err)
		}
	}
}

// TestManifest は、宣言ファイルが検査を通り、宣言したテキストオブジェクトの ID が登録した ID と一致することを確かめる。
func TestManifest(t *testing.T) {
	data, err := os.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m ext.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("manifest.json: %v", err)
	}
	if warnings, verr := m.Validate(); verr != nil || len(warnings) > 0 {
		t.Fatalf("Validate = %v, %v; want no warnings and no error", warnings, verr)
	}
	if m.Command != "./modal-editor-ext-line" {
		t.Errorf("command = %q, want ./modal-editor-ext-line", m.Command)
	}
	var ids []string
	for _, c := range m.Contributes.TextObjects {
		ids = append(ids, c.ID)
	}
	slices.Sort(ids)
	if want := []string{idAround, idInner}; !slices.Equal(ids, want) {
		t.Errorf("textObjects = %v, want %v", ids, want)
	}
}

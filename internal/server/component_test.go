package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/protocol"
)

// 中央のポップアップを開いた編集の client がペインを知っていれば、server は Config.OpenPopup で部品の client を起動させる。
// ソケットで繋いだポップアップの部品の client（Hello の View）は、部品を開いた元の client に繋がり、部品の画面を受け取る。
// 部品の client で打ったキーは元の client の操作になり、<Esc> で部品を閉じると、部品の client は理由の無い終わりの知らせを受ける。
//
// req: FR-67
func TestServeComponentClient(t *testing.T) {
	views := make(chan uint64, 4)
	srv, err := New(editor.Config{OpenPopup: func(_ string, view uint64, _ string) error {
		views <- view
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Close(ctx)
	})
	sock := filepath.Join(privateDir(t), "s", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln, nil) }()
	c, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err = c.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 60, Height: 12, Pane: "%1"}); err != nil {
		t.Fatal(err)
	}
	waitScreen(t, c, "[No Name]")
	sendKeys(t, c, ":h K<CR>")
	var view uint64
	select {
	case view = <-views:
	case <-time.After(10 * time.Second):
		t.Fatal("OpenPopup was not called")
	}

	popup, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = popup.Close() }()
	if err = popup.Send(protocol.Hello{Kind: protocol.KindPopup, Width: 50, Height: 10, View: view}); err != nil {
		t.Fatal(err)
	}
	d, scr := recvType[protocol.Draw](t, popup)
	if d.Screen.Grid.Width() != 50 || !strings.Contains(scr, "K") {
		t.Fatalf("popup screen (%d columns):\n%s", d.Screen.Grid.Width(), scr)
	}
	sendKeys(t, popup, "<Esc>")
	if ex, _ := recvType[protocol.Exit](t, popup); ex.Reason != "" {
		t.Errorf("popup client exit = %+v, want no reason", ex)
	}
	// 閉じた後は、元の client で編集を続けられる。
	sendKeys(t, c, "iok<Esc>")
	waitScreen(t, c, "ok")
	sendKeys(t, c, ":q!<CR>")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Serve did not return")
	}
}

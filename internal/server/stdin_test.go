package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/protocol"
)

// recvScreen は、c に届くメッセージを、画面に want を含む Draw が届くまで読み、その画面を返す。
// 10 秒のうちに届かなければ、最後の画面を付けて落とす。本体の誤りで want が来ないときに、テストの実行の期限まで止まらないためである（#313）。
func recvScreen(t *testing.T, c protocol.ClientConn, want string) string {
	t.Helper()
	type got struct {
		m   protocol.ServerMessage
		err error
	}
	var last string
	deadline := time.After(10 * time.Second)
	for {
		ch := make(chan got, 1)
		go func() {
			m, err := c.Recv()
			ch <- got{m, err}
		}()
		select {
		case g := <-ch:
			if g.err != nil {
				t.Fatalf("%q を含む画面の前に Recv = %v。最後の画面:\n%s", want, g.err, last)
			}
			if d, ok := g.m.(protocol.Draw); ok {
				last = d.Screen.Grid.String()
				if strings.Contains(last, want) {
					return last
				}
			}
		case <-deadline:
			t.Fatalf("%q を含む画面が 10 秒のうちに届かない。最後の画面:\n%s", want, last)
		}
	}
}

// server のソケットで繋いだ編集の client が Hello の Stdin で送った中身（FR-158）は、名前の無いバッファのタブとして開き、
// 未保存の変更がある扱いにする（:q は E37）。最初の client も、後から繋いだ client（既にある server に繋いだとき）も同じである。
// 待つだけの client（:! の中の vi-imitation -）の中身は、:! を実行した client のタブに開く。
//
// req: FR-158
func TestServeOpensHelloStdin(t *testing.T) {
	srv := newServer(t)
	sock := filepath.Join(privateDir(t), "s", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln, nil) }()

	first, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	text := "hoge\nあいう\n"
	if err = first.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 40, Height: 10, Stdin: &text}); err != nil {
		t.Fatal(err)
	}
	if scr := recvScreen(t, first, "あいう"); !strings.Contains(scr, "[No Name] [+]") || !strings.Contains(scr, "hoge") {
		t.Errorf("最初の client の画面に、変更のある名前の無いバッファの中身が無い:\n%s", scr)
	}
	sendKeys(t, first, ":q<CR>")
	recvScreen(t, first, "E37")

	second, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	text2 := "second\n"
	if err = second.Send(protocol.Hello{Kind: protocol.KindEdit, Width: 40, Height: 10, Stdin: &text2}); err != nil {
		t.Fatal(err)
	}
	if scr := recvScreen(t, second, "second"); !strings.Contains(scr, "[No Name] [+]") {
		t.Errorf("後から繋いだ client の画面に、変更のある名前の無いバッファが無い:\n%s", scr)
	}

	waited, err := protocol.Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = waited.Close() }()
	text3 := "from wait\n"
	if err = waited.Send(protocol.Hello{Kind: protocol.KindWait, Parent: 2, Stdin: &text3}); err != nil {
		t.Fatal(err)
	}
	recvScreen(t, second, "from wait")
	sendKeys(t, second, ":q!<CR>")
	if m, err := waited.Recv(); err != nil {
		t.Errorf("待つだけの client の Recv = %v", err)
	} else if ex, ok := m.(protocol.Exit); !ok || ex.Reason != "" {
		t.Errorf("待つだけの client の Recv = %#v, want 理由の無い Exit", m)
	}

	sendKeys(t, second, ":q!<CR>")
	sendKeys(t, first, ":q!<CR>")
	if err := <-done; err != nil {
		t.Errorf("Serve = %v", err)
	}
}

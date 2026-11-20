package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/internal/protocol"
	"github.com/piplupflipper/vi-imitation/internal/rpc"
)

// Hello を待つ期限は、何も読めない時間で数え、読めている間は延びる（Hello に続く標準入力の中身。FR-158）。
// 何も読めないまま期限が経つか、done が閉じたら取り消される。
//
// req: FR-158
func TestHelloContext(t *testing.T) {
	const limit = 100 * time.Millisecond
	a, b := net.Pipe()
	defer func() { _ = a.Close() }()
	defer func() { _ = b.Close() }()
	var shared progress
	pc := newProgressConn(a, &shared)
	ctx, cancel := helloContext(pc, limit, make(chan struct{}))
	defer cancel()
	go func() {
		buf := make([]byte, 16)
		for {
			if _, err := pc.Read(buf); err != nil {
				return
			}
		}
	}()
	// 期限の 3 倍の間、期限より短い間隔で読ませ続けても、取り消されない。
	for end := time.Now().Add(3 * limit); time.Now().Before(end); {
		if _, err := b.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(limit / 5)
	}
	if ctx.Err() != nil {
		t.Fatal("読めている間に取り消された")
	}
	if d := shared.idleSince(time.Time{}); d > limit {
		t.Errorf("共通の時刻が読めた時刻にならない（%v 前）", d)
	}
	select {
	case <-ctx.Done():
	case <-time.After(10 * limit):
		t.Fatal("読めなくなってから期限が経っても取り消されない")
	}

	done := make(chan struct{})
	ctx2, cancel2 := helloContext(newProgressConn(a, nil), time.Hour, done)
	defer cancel2()
	close(done)
	select {
	case <-ctx2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("done を閉じても取り消されない")
	}
}

// 最初の編集の client を待つ期限（FirstClientTimeout）も、何も読めない時間で数える。
// 期限より長くかけて Hello に続く標準入力の中身を送る client でも、server は ErrNoClient で終わらずに、その client の中身を開く
// （PR #304 のレビューの必須 1。新しい tmux の window で最初に打った git log | vi-imitation - が、遅い送りで server を終わらせた）。
// 誰も何も送らなければ、今までどおり期限で ErrNoClient で戻る。
//
// req: FR-158
func TestServeFirstClientSlowStdin(t *testing.T) {
	const limit = 300 * time.Millisecond
	srv := newServer(t)
	srv.FirstClientTimeout = limit
	sock := filepath.Join(privateDir(t), "s", SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(context.Background(), ln, nil) }()

	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	write := func(body string) {
		t.Helper()
		if werr := rpc.WriteMessage(c, []byte(body)); werr != nil {
			t.Fatal(werr)
		}
	}
	write(`{"jsonrpc":"2.0","method":"client/hello","params":{"kind":"edit","width":40,"height":20,"stdin":true}}`)
	// 期限の 4 倍の時間をかけて、期限の 1/3 ごとに 1 行ずつ送る。
	const n = 12
	for i := range n {
		time.Sleep(limit / 3)
		data := base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "line%02d\n", i))
		write(fmt.Sprintf(`{"jsonrpc":"2.0","method":"client/stdin","params":{"data":%q,"rest":%d}}`, data, 7*(n-1-i)))
	}
	conn := protocol.NewClientSocket(c)
	if scr := recvScreen(t, conn, "line11"); !strings.Contains(scr, "line00") {
		t.Errorf("遅く送った中身の画面に、最初の行が無い:\n%s", scr)
	}
	select {
	case serr := <-done:
		t.Fatalf("中身を開いた後に Serve = %v で戻った", serr)
	default:
	}
	sendKeys(t, conn, ":q!<CR>")
	if serr := <-done; serr != nil {
		t.Errorf("Serve = %v, want nil", serr)
	}

	idle := newServer(t)
	idle.FirstClientTimeout = limit
	ln2, err := Listen(filepath.Join(privateDir(t), "s", SocketName))
	if err != nil {
		t.Fatal(err)
	}
	if err := idle.Serve(context.Background(), ln2, nil); !errors.Is(err, ErrNoClient) {
		t.Errorf("誰も繋がない Serve = %v, want ErrNoClient", err)
	}
}

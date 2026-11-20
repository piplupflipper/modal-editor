package server

import (
	"net"
	"testing"
	"time"
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
	pc := newProgressConn(a)
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
	select {
	case <-ctx.Done():
	case <-time.After(10 * limit):
		t.Fatal("読めなくなってから期限が経っても取り消されない")
	}

	done := make(chan struct{})
	ctx2, cancel2 := helloContext(newProgressConn(a), time.Hour, done)
	defer cancel2()
	close(done)
	select {
	case <-ctx2.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("done を閉じても取り消されない")
	}
}

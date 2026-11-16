package main

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

// flakyListener は、最初の fails 回の Accept で一時的な誤りを返し、その後は net.ErrClosed を返す待ち受けである。
type flakyListener struct {
	net.Listener
	fails   int32
	accepts atomic.Int32
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if l.accepts.Add(1) <= l.fails {
		return nil, errors.New("accept: too many open files")
	}
	return nil, net.ErrClosed
}

// 閉じた以外の誤りでは、受け付けをやめずに、待ってから受け付け直す。
func TestAcceptLoopRetries(t *testing.T) {
	x := newExtension("", nil, nil, nil)
	ln := &flakyListener{fails: 2}
	done := make(chan struct{})
	go func() {
		x.acceptLoop(context.Background(), ln)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("acceptLoop が戻らない")
	}
	if n := ln.accepts.Load(); n != 3 {
		t.Errorf("Accept を %d 回呼んだ, want 3（誤り 2 回の後に受け付け直す）", n)
	}
}

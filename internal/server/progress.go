package server

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// progressConn は、最後に読めたバイトがあった時刻を覚える net.Conn である。
// admit が Hello を待つ期限を、読めている間は延ばすのに使う（helloContext）。
type progressConn struct {
	net.Conn
	last atomic.Int64 // 最後に読めた時刻（UnixNano）。読む前は受け付けた時刻
}

func newProgressConn(c net.Conn) *progressConn {
	pc := &progressConn{Conn: c}
	pc.last.Store(time.Now().UnixNano())
	return pc
}

func (c *progressConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.last.Store(time.Now().UnixNano())
	}
	return n, err //nolint:wrapcheck // net.Conn の Read の誤りを、そのまま読む側（internal/protocol）へ返す
}

// idle は、最後に読めてから経った時間を返す。
func (c *progressConn) idle() time.Duration {
	return time.Since(time.Unix(0, c.last.Load()))
}

// helloContext は、接続 pc の Hello を待つ間の ctx を返す。何も読めないまま limit が経つか、done が閉じたら取り消される。
//
// Hello の後に標準入力の中身（protocol.Hello.Stdin。FR-158）が続くと、数百 MB の中身を受け終えるまでに limit を超えることがある
// （2026-11-20 の手元の計測で、300 MiB を送るのに約 1.6 秒かかった）。読めている間は期限を延ばし、Hello を送らない接続だけを limit で断る。
// cancel は、待ち終えたら呼ぶ。
func helloContext(pc *progressConn, limit time.Duration, done <-chan struct{}) (ctx context.Context, cancel func()) {
	ctx, cancel = context.WithCancel(context.Background())
	go func() {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		for {
			select {
			case <-done:
				cancel()
				return
			case <-ctx.Done():
				return
			case <-timer.C:
				idle := pc.idle()
				if idle >= limit {
					cancel()
					return
				}
				timer.Reset(limit - idle)
			}
		}
	}()
	return ctx, cancel
}

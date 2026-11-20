package server

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// progress は、最後に読めたバイトがあった時刻（UnixNano）である。0 なら、まだ読めていない。
type progress struct {
	last atomic.Int64
}

// touch は、今を読めた時刻にする。
func (p *progress) touch() { p.last.Store(time.Now().UnixNano()) }

// idleSince は、最後に読めてから（まだ読めていなければ since から）経った時間を返す。
func (p *progress) idleSince(since time.Time) time.Duration {
	last := since
	if n := p.last.Load(); n != 0 {
		if t := time.Unix(0, n); t.After(last) {
			last = t
		}
	}
	return time.Since(last)
}

// progressConn は、読めたバイトがあった時刻を、接続ごとの own と、受け付けたすべての接続に共通の shared に覚える net.Conn である。
// admit が Hello を待つ期限（helloContext）と、Serve が最初の編集の client を待つ期限（firstClient）を、読めている間は延ばすのに使う。
type progressConn struct {
	net.Conn
	own    progress
	shared *progress // nil なら覚えない
}

func newProgressConn(c net.Conn, shared *progress) *progressConn {
	pc := &progressConn{Conn: c, shared: shared}
	pc.own.touch() // Hello を待つ期限は、受け付けた時刻から数える
	return pc
}

func (c *progressConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.own.touch()
		if c.shared != nil {
			c.shared.touch()
		}
	}
	return n, err //nolint:wrapcheck // net.Conn の Read の誤りを、そのまま読む側（internal/protocol）へ返す
}

// idleExpired は、idle が limit 以上になったら閉じるチャネルを返す。stop が閉じたら見るのをやめる（チャネルは閉じない）。
// idle は、何も読めていない時間を返す関数である。
func idleExpired(limit time.Duration, idle func() time.Duration, stop <-chan struct{}) <-chan struct{} {
	expired := make(chan struct{})
	go func() {
		timer := time.NewTimer(limit)
		defer timer.Stop()
		for {
			select {
			case <-stop:
				return
			case <-timer.C:
				d := idle()
				if d >= limit {
					close(expired)
					return
				}
				timer.Reset(limit - d)
			}
		}
	}()
	return expired
}

// helloContext は、接続 pc の Hello を待つ間の ctx を返す。何も読めないまま limit が経つか、done が閉じたら取り消される。
//
// Hello の後に標準入力の中身（protocol.Hello.Stdin。FR-158）が続くと、数百 MB の中身を受け終えるまでに limit を超えることがある
// （2026-11-20 の手元の計測で、300 MiB を送るのに約 1.6 秒かかった）。読めている間は期限を延ばし、何も送らない接続だけを limit で断る。
// cancel は、待ち終えたら呼ぶ。
func helloContext(pc *progressConn, limit time.Duration, done <-chan struct{}) (ctx context.Context, cancel func()) {
	ctx, cancel = context.WithCancel(context.Background())
	start := time.Now()
	expired := idleExpired(limit, func() time.Duration { return pc.own.idleSince(start) }, ctx.Done())
	go func() {
		select {
		case <-done:
			cancel()
		case <-expired:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

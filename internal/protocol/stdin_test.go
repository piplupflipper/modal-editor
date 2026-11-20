package protocol

import (
	"net"
	"strings"
	"testing"
)

// Hello の Stdin（FR-158）は、ソケットの上では塊に分けて送り、受ける側は塊をすべてつないでから Hello を返す。
// 空の中身、正しくない UTF-8、塊の境目をまたぐ大きさ、Stdin の無い Hello を、そのまま往復させる。
// Hello の後の通知（Event）は、中身の塊と取り違えない。
//
// req: FR-158
func TestSocketHelloStdin(t *testing.T) {
	big := strings.Repeat("0123456789abcde\n", stdinChunkSize/16*2) + "tail"
	for _, tt := range []struct {
		name  string
		stdin *string
	}{
		{"Stdin の無い Hello", nil},
		{"空の中身", ptr("")},
		{"正しくない UTF-8", ptr("a\xffb\n\xe3\x81")},
		{"塊の境目をまたぐ", ptr(big)},
		{"塊の大きさちょうど", ptr(strings.Repeat("x", stdinChunkSize))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, s := socketPair(t)
			sent := make(chan error, 1)
			go func() {
				err := c.Send(Hello{Kind: KindEdit, Width: 80, Height: 24, Stdin: tt.stdin})
				if err == nil {
					err = c.Send(Event{Paste: "after"})
				}
				sent <- err
			}()
			m, err := s.Recv()
			if err != nil {
				t.Fatal(err)
			}
			h, ok := m.(Hello)
			switch {
			case !ok:
				t.Fatalf("server Recv = %#v, want Hello", m)
			case (h.Stdin == nil) != (tt.stdin == nil):
				t.Fatalf("Hello.Stdin = %v, want %v", h.Stdin, tt.stdin)
			case h.Stdin != nil && *h.Stdin != *tt.stdin:
				t.Fatalf("Hello.Stdin の長さ = %d, want %d（中身が違う）", len(*h.Stdin), len(*tt.stdin))
			case h.Width != 80 || h.Kind != KindEdit:
				t.Fatalf("Hello = %#v", h)
			}
			if m, err := s.Recv(); err != nil || m != (Event{Paste: "after"}) {
				t.Fatalf("Hello の後の Recv = %#v, %v; want the event", m, err)
			}
			if err := <-sent; err != nil {
				t.Fatal(err)
			}
		})
	}
}

// 中身の塊の途中に別の通知が来るか、続くバイト数が合わなければ、Hello を返さずに誤りにする。
//
// req: FR-158
func TestSocketHelloStdinBroken(t *testing.T) {
	hello := `{"jsonrpc":"2.0","method":"client/hello","params":{"kind":"edit","stdin":true}}`
	for name, bodies := range map[string][]string{
		"塊の途中に別の通知":   {hello, `{"jsonrpc":"2.0","method":"client/stdin","params":{"data":"YQ==","rest":3}}`, `{"jsonrpc":"2.0","method":"client/event","params":{"paste":"p"}}`},
		"続くバイト数が合わない": {hello, `{"jsonrpc":"2.0","method":"client/stdin","params":{"data":"YQ==","rest":3}}`, `{"jsonrpc":"2.0","method":"client/stdin","params":{"data":"YQ==","rest":1}}`},
		"負の続くバイト数":    {hello, `{"jsonrpc":"2.0","method":"client/stdin","params":{"data":"YQ==","rest":-1}}`},
		"桁あふれする大きさ":   {hello, `{"jsonrpc":"2.0","method":"client/stdin","params":{"data":"YQ==","rest":9223372036854775807}}`},
	} {
		t.Run(name, func(t *testing.T) {
			a, b := net.Pipe()
			s := NewServerSocket(a)
			defer func() { _ = s.Close() }()
			writeRaw(t, b, bodies...)
			if m, err := s.Recv(); err == nil {
				t.Fatalf("server Recv = %#v, want an error", m)
			}
		})
	}
}

func ptr(s string) *string { return &s }

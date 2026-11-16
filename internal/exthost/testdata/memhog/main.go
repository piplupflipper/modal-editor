// memhog は、メモリの上限（FR-20、RLIMIT_AS）のテストに使う拡張機能である（rlimit_test.go）。
//
// テストの実行ファイル自身（fakeext_test.go）を拡張機能にすると、-race でビルドしたときに
// データ競合の検出器が起動時に大きな仮想アドレス空間を予約し、上限の下では起動できない。
// このため、テストの前に -race を付けずにビルドして使う。
//
// initialize と ping に応答し、shutdown で終わる。テスト専用のリクエストは次のとおりである。
//   - test/pid: 自分のプロセス ID を返す
//   - test/grandchild: 子プロセス（sleep）を起動し、そのプロセス ID を返す
//   - test/alloc: 64 MiB ずつ、最大 64 回確保する。確保できたら確保したバイト数を返す
package main

import (
	"context"
	"os"
	"os/exec"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/rpc"
)

// allocChunk は、test/alloc で 1 回に確保するバイト数である。
const allocChunk = 64 << 20

// allocMaxChunks は、test/alloc で確保する回数の上限である。
// 上限が掛かっていないとき（テストの失敗）に、確保し続けないためのものである。
// 確保した領域には書き込まないので、実際に使うメモリはほとんど増えない。
const allocMaxChunks = 64

// allocated は、test/alloc で確保した領域を GC から守るために持ち続ける。
var allocated [][]byte

func main() {
	conn := rpc.NewConn(os.Stdin, os.Stdout, rpc.HandlerFuncs{
		Request: func(_ context.Context, req *rpc.Request, reply rpc.Replier) {
			switch req.Method {
			case ext.MethodInitialize:
				_ = reply(ext.InitializeResult{APIVersion: ext.APIVersion}, nil)
			case ext.MethodPing:
				_ = reply(ext.PingResult{}, nil)
			case "test/pid":
				_ = reply(os.Getpid(), nil)
			case "test/grandchild":
				cmd := exec.Command("sleep", "1000")
				if err := cmd.Start(); err != nil {
					_ = reply(nil, err)
					return
				}
				_ = reply(cmd.Process.Pid, nil)
			case "test/alloc":
				for range allocMaxChunks {
					allocated = append(allocated, make([]byte, allocChunk))
				}
				_ = reply(len(allocated)*allocChunk, nil)
			default:
				_ = reply(nil, &rpc.Error{Code: rpc.CodeMethodNotFound, Message: req.Method})
			}
		},
		Notification: func(_ context.Context, n *rpc.Notification) {
			if n.Method == ext.MethodShutdown {
				os.Exit(0)
			}
		},
	})
	<-conn.Done()
}

// Command modal-editor-ext-dap は、DAP（Debug Adapter Protocol）のクライアントとしてデバッグを行う拡張機能である（基本設計 6.4、FR-31）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json のコマンドが呼ばれたときに子プロセスとして起動する。
// 本体とは標準入出力で JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
//
// dap.start で、設定に書いたデバッグアダプター（既定は delve の dlv dap）を子プロセスとして起動し、DAP で話す。
// DAP のメッセージの区切りは Content-Length ヘッダーの形式だが、中身は JSON-RPC ではなく DAP 独自の
// request、response、event の形なので、送受信は dap.go に書く。
// 仕様: https://microsoft.github.io/debug-adapter-protocol/specification
//
// コマンド（名前は仮）は次のとおりで、どれも command/execute には受け付けたかどうかだけを ok で返し、
// 結果は後から sign/set、editor/open、message/show、output/append で届ける（基本設計 6 章の冒頭）。
//
//   - dap.toggleBreakpoint: カーソルの行のブレークポイントを切り替える
//   - dap.start、dap.stop: デバッグを始める、終える
//   - dap.continue、dap.next、dap.stepIn、dap.stepOut: 続行とステップ実行
//
// 状態の持ち方と、本体との取り決めの詳細は debugger の doc に、DAP の手順は session の doc にある。
// 設定（initialize の settings）の項目は settings の doc にある。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("modal-editor-ext-dap: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
// 終わるときに、デバッグ中ならデバッグアダプターを終わらせる。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := newDebugger()
	defer d.shutdown()
	return d.srv.Run(ctx)
}

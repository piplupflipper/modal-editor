// Command modal-editor-ext-mcp は、エディタの状態と編集の操作を MCP（Model Context Protocol）のツールとして提供する拡張機能である（基本設計 6.2、FR-32）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json の起動の契機 socket（ext.ActivationSocket）で、
// MCP のソケットへの最初の接続を受け付けたときに子プロセスとして起動する（FR-14）。
// 本体とは標準入出力で JSON-RPC のメッセージ（Content-Length の区切り）をやり取りするので、ログは標準エラー出力へ書く。
//
// # MCP の接続
//
// MCP のクライアント（コーディングエージェント）は、中継コマンド modal-editor-mcp-bridge を標準入出力の MCP サーバーとして起動し、
// 中継コマンドが Unix ドメインソケットでこの拡張機能へ接続する（FR-33）。
// 1 本の接続の上の MCP のメッセージは、MCP の stdio と同じ改行区切りの JSON-RPC 2.0 である（serveMCP）。
// serveMCP は 1 本の io.ReadWriteCloser だけを受け取り、接続をどう受け取ったかには依らない。
//
// 接続の受け取り方は、基本設計 6.2 の起動の契機 socket（#43 の裁定 4）である。
//
//   - 本体は、環境変数 MODAL_EDITOR_MCP_SOCKET（ext.EnvMCPSocket。中継コマンドと同じ名前）のパスで起動時から待ち受ける。
//     パスは本体が起動ごとに決めて、すべての拡張機能へ渡す（$XDG_RUNTIME_DIR/modal-editor/<pid>-<乱数>/mcp.sock）。
//     ACP の拡張機能は同じ変数から中継コマンドの引数を作るので、ACP で起動したエージェントはこの拡張機能へつながる
//   - 本体は、最初の接続を受け付けた時点でこの拡張機能を起動し、待ち受けているソケットと受け付けた接続のファイル記述子を引き継ぐ。
//     拡張機能は ext/server の InheritedSocket で net.Listener と net.Conn にし、initialize を受け取った時点で、
//     引き継いだ接続の処理と、待ち受けでの受け付けを始める。拡張機能は自分ではソケットを作らない
//   - コマンド mcp.start（仮）は、待ち受けているパスか、引き継げなかった理由を、利用者へのメッセージで知らせる。
//     本体がパスを決められなかったか待ち受けられなかったときは、socket の契機では起動しないので、mcp.start で起動して理由を知る
//
// # MCP の版
//
// 2026-11-16 に https://modelcontextprotocol.io/specification で確かめた版のうち、次の 2 つを扱う（mcpSession）。
//
//   - 2026-07-28（最新）: 各リクエストの params._meta に版とクライアントの能力を載せ、初期化の手順を持たない
//   - 2025-11-25: initialize と notifications/initialized で版を取り決める。この版までのクライアントのために扱う
//
// # ツール
//
// ツールは基本設計 6.2 の表のとおりで、本体へのメッセージ（editor/state、diagnostic/list、buffer/edit、editor/open、buffer/save）と、
// 拡張機能が持つバッファの複製で実装する（tools.go）。
// ツールの引数と結果の位置は、行を 1 から、列を行の先頭から 1 から数えたバイト数で表す（Vim の line() と col() と同じ）。
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/piplupflipper/modal-editor/ext/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("modal-editor-ext-mcp: %v", err)
	}
}

// run は、本体から shutdown を受け取るか、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
// 終わるときに、ソケットの待ち受けと MCP の接続を閉じる。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ln, conns, err := server.InheritedSocket()
	if err != nil {
		log.Printf("modal-editor-ext-mcp: inherited socket: %v", err)
	}
	x := newExtension(os.Getenv(socketEnv), ln, conns, err)
	defer x.stop()
	return x.srv.Run(ctx)
}

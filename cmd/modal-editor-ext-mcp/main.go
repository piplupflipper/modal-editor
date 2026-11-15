// Command modal-editor-ext-mcp は、エディタの状態と編集の操作を MCP（Model Context Protocol）のツールとして提供する拡張機能である（基本設計 6.2、FR-32）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json のコマンド mcp.start が呼ばれたときに子プロセスとして起動する。
// 本体とは標準入出力で JSON-RPC のメッセージ（Content-Length の区切り）をやり取りするので、ログは標準エラー出力へ書く。
//
// # MCP の接続
//
// MCP のクライアント（コーディングエージェント）は、中継コマンド modal-editor-mcp-bridge を標準入出力の MCP サーバーとして起動し、
// 中継コマンドが Unix ドメインソケットでこの拡張機能へ接続する（FR-33）。
// 1 本の接続の上の MCP のメッセージは、MCP の stdio と同じ改行区切りの JSON-RPC 2.0 である（serveMCP）。
// serveMCP は 1 本の io.ReadWriteCloser だけを受け取り、接続をどう受け取ったかには依らない。
//
// 接続の受け取り方は、基本設計 6.2 の `socket` の起動の契機（本体が待ち受けて、最初の接続で拡張機能を起動し、
// ファイル記述子を引き継ぐ）が裁定待ちなので、当面は次の形にしている（listenSocket）。
//
//   - 起動の契機はコマンド mcp.start（仮）である。利用者がキーマップでキーを割り当てて呼ぶ
//   - 拡張機能は、initialize を受け取った時点で、環境変数 MODAL_EDITOR_MCP_SOCKET（中継コマンドと同じ名前）のパスで待ち受ける。
//     子プロセスは本体の環境変数を引き継ぐので、エディタを起動する前にこの変数を設定しておく
//   - mcp.start は、待ち受けているパスか、待ち受けられなかった理由を、利用者へのメッセージで知らせる
//
// 裁定の後は、本体が引き継いだファイル記述子から net.FileListener と net.FileConn で接続を作り、serveMCP へ渡す形に替える。
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
	x := newExtension(os.Getenv)
	defer x.stop()
	return x.srv.Run(ctx)
}

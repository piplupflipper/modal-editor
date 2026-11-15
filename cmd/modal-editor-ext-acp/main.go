// Command modal-editor-ext-acp は、ACP（Agent Client Protocol）のクライアントとして、コーディングエージェントを
// 子プロセスで起動して話す拡張機能である（基本設計 6.5、FR-34）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json のコマンドが呼ばれたときに子プロセスとして起動する。
// 本体とは標準入出力で JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
//
// # ACP の版
//
// 2026-11-16 に https://agentclientprotocol.com の protocol の各ページで確かめた、プロトコルの版 1
// （initialize の protocolVersion が 1）を実装する。メッセージの型は protocol.go にある。
// エージェントとは標準入出力で、1 件を 1 行にして改行で区切った JSON-RPC 2.0 で話す（ACP の Transports。conn.go の lineConn）。
//
// # コマンド
//
// コマンド（名前は仮）は次のとおりで、どちらも command/execute には受け付けたかどうかだけを ok で返し、
// 結果は後から output/append の acp の欄と message/show で届ける（基本設計 6 章の冒頭）。
//
//   - acp.prompt: args の {"text": "..."} をプロンプトとして送る。エージェントとセッションが無ければ、先に起動して作る
//   - acp.cancel: 処理中のプロンプトを取り消す
//
// # エージェントとのやり取り
//
// 起動したエージェントへは initialize でファイルの読み書きの能力を申し出て、session/new で、作業ディレクトリと、
// MCP の中継コマンドを MCP サーバーとして渡す（client.mcpServers）。
// エージェントからの session/update は acp の欄へ書き足し、session/request_permission は choice/ask で利用者に問い、
// fs/read_text_file はバッファの複製かファイルから読み、fs/write_text_file はバッファを通して書き出す（handlers.go、files.go）。
// 開いていないファイルへの書き込みの方式は #43 の 3 の裁定待ちで、差し替えられる口（unopenedFileWriter）にしてある。
//
// エージェントが異常終了したら、利用者へ知らせ、次の acp.prompt で起動し直す。
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
		log.Fatalf("modal-editor-ext-acp: %v", err)
	}
}

// run は、本体から shutdown を受け取るか、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
// 終わるときに、エージェントを終わらせる。shutdown のときは、接続を閉じる前に OnShutdown のフックで終わらせる。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c := newClient()
	defer c.stop()
	return c.srv.Run(ctx)
}

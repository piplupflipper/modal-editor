// Command modal-editor-ext-lsp は、言語サーバーを子プロセスとして起動し、LSP で話す拡張機能である（基本設計 6.3）。
//
// 本体（internal/exthost）が、言語サーバーのあるファイルの種類のファイルを開いたときに起動する（宣言ファイル manifest.json の filetype の契機）。
// 標準入出力で本体と JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
// 言語サーバーの標準エラー出力も、そのまま標準エラー出力へ流す。
//
// # 言語サーバーの設定
//
// どのファイルの種類にどの言語サーバーを使うかは、利用者の設定のうちこの拡張機能に向けた部分（initialize の settings）に書く。
// 形は settings 型にある。既定では Go（ファイルの種類 "go"）に gopls を使う。
// 1 つのプロセスで、ファイルの種類ごとに言語サーバーを 1 つ起動する（client 型）。
// 言語サーバーは、そのファイルの種類の最初のファイルを開いたときに起動する。
// 利用者が設定で言語を足したときの起動の契機は、利用者の設定から宣言ファイルの activation へ足す（基本設計 6.7）。
//
// # 機能
//
//   - 文書の同期: buffer/open、buffer/change、buffer/close を textDocument/didOpen、didChange（増分）、didClose にする
//   - 診断: textDocument/publishDiagnostics を diagnostic/set にする
//   - 定義へ移動: コマンド lsp.definition。textDocument/definition の結果へ editor/open で移動する。候補が複数なら choice/ask で選ばせる
//   - ホバー: コマンド lsp.hover。textDocument/hover の結果を popup/show で表示させる
//   - 整形: コマンド lsp.format と、保存の前（buffer/willSave。宣言ファイルの saveHook）。textDocument/formatting の結果をトランザクションにする
//   - 保存の通知: buffer/didSave を textDocument/didSave にする
//
// コマンドの既定のキーは決めない（基本設計 6.3）。補完は作らない（裁定待ち）。
//
// # 位置の変換
//
// 本体の位置は行とバイト数で、LSP の位置は行と、サーバーが選んだ数え方（既定は UTF-16）の文字の位置である。
// 変換はこの拡張機能の中で行う（基本設計 4.1）。initialize では utf-8 を先に申し出る（基本設計 6.3）。
//
// # 言語サーバーの異常終了
//
// 言語サーバーが異常終了しても、この拡張機能は動き続ける。
// 回数の上限（既定は 3 回。仮）までは、待ってから再起動し、開いている文書の全文を送り直す。
// 上限に達したら、そのファイルの種類の言語サーバーをもう起動せず、message/show で知らせる。
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
		log.Fatalf("modal-editor-ext-lsp: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
// 終わるときに、起動した言語サーバーを止める。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	x := newExtension(ctx)
	err := x.srv.Run(ctx)
	x.stop()
	return err
}

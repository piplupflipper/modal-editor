// Command modal-editor-ext-line は、行のテキストオブジェクトを提供する拡張機能である。
//
// 標準の拡張機能の 1 本目で、要件定義 8 章の段階 1 で「拡張機能が提供するテキストオブジェクトを、別のプロセス経由で動かす」ために置く。
// 本体（internal/exthost）が子プロセスとして起動し、標準入出力で JSON-RPC のメッセージをやり取りする。
// 標準出力はメッセージに使うので、ログは標準エラー出力へ書く。
//
// 提供するテキストオブジェクトは次の 2 つで、宣言ファイルは同じディレクトリの manifest.json である。
//
//   - line.inner（既定のキーは il）: カーソル行から、行の先頭と末尾の空白を除いた範囲
//   - line.around（既定のキーは al）: カーソル行の全体。行末の改行は含めない
//
// 範囲の決め方の詳細は resolveInner と resolveAround の doc にある。
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
		log.Fatalf("modal-editor-ext-line: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newServer().Run(ctx)
}

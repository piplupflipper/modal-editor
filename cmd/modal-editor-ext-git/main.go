// Command modal-editor-ext-git は、git 連携の標準の拡張機能である（要件定義 4.14。FR-104。基本設計 6.9）。
//
// 本体（internal/exthost）が子プロセスとして起動し、標準入出力で JSON-RPC のメッセージをやり取りする。
// 標準出力はメッセージに使うので、ログは標準エラー出力へ書く。宣言ファイルは同じディレクトリの manifest.json である。
//
// 提供するコマンドは次の 1 つである（名前と既定のキーは仮）。
//
//   - git.diffHead（既定のキーは <Leader>gd）: カーソルのあるバッファのファイルの、git の HEAD の版を、
//     編集を禁止したファイルでないバッファとして開き（buffer/create。FR-88）、そのバッファと見比べる（diff/start。FR-99）。
//     HEAD の版のバッファは、端末多重化ソフトのペインを分割した新しい編集の client のタブになる（基本設計 6.8）
//
// 行の印、blame など、この他の範囲は要件定義 7 章の未決事項で、持たない。
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
		log.Fatalf("modal-editor-ext-git: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newExtension().server.Run(ctx)
}

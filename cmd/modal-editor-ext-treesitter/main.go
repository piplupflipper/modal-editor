// Command modal-editor-ext-treesitter は、tree-sitter で構文を解析し、色を付ける範囲の一覧を本体へ送る拡張機能である（基本設計 6.1）。
//
// 本体（internal/exthost）が、文法を持つ種類のファイルを開いたときに子プロセスとして起動する（宣言ファイル manifest.json の filetype の契機）。
// 標準入出力で JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
//
// 動きは次のとおりである。
//
//   - buffer/open で全文を解析し、色を付ける範囲の一覧を decoration/set で送る
//   - buffer/change の編集を 1 つずつ構文木（Tree.Edit）と手元の全文に適用し、前の構文木を渡して増分で解析し直し、一覧を送り直す
//   - buffer/close で構文木を捨てる
//
// 解析と一覧の計算は、メッセージを処理する goroutine ではなく 1 つの解析の goroutine で行う。
// 解析の間に溜まった差分はまとめて 1 回で解析し直し、途中のバージョンの一覧は送らない（highlighter の doc）。
// 一覧が大きすぎるバッファ（maxDecorationSetBytes）には色を付けない。
//
// 対応する言語は Go（ファイルの種類 "go"）と JSON（"json"）で、表は languages にある。
// 文法は公式の Go のモジュール（github.com/tree-sitter/tree-sitter-go など）を cgo で一緒にコンパイルする。
// cgo を使うのはこの実行ファイルだけで、本体は cgo を使わない（NFR-02）。
//
// ハイライトのクエリは、各文法の公式の queries/highlights.scm を queries/ の下に写して埋め込む。
// キャプチャ名から decoration/set の色の種類の名前への対応は styleFor にある。
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
		log.Fatalf("modal-editor-ext-treesitter: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	h, err := newHighlighter()
	if err != nil {
		return err
	}
	defer h.close()
	return h.server().Run(ctx)
}

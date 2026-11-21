// Command vi-imitation-ext-formatter は、外部の整形ツール（biome、ruff、stylua、shfmt など）で整形する標準の拡張機能である
// （要件定義 4.3。FR-175、FR-176。基本設計 6.10）。
//
// 本体（internal/exthost）が子プロセスとして起動し、標準入出力で JSON-RPC のメッセージをやり取りする。
// 標準出力はメッセージに使うので、ログは標準エラー出力へ書く。宣言ファイルは同じディレクトリの manifest.json である。
//
// # 何をするか
//
//   - 保存の前（buffer/willSave。基本設計 6.7 の保存の流れ）に、バッファのファイルの種類に合う整形ツールを走らせ、
//     その結果を、元の本文との行の差分の塊ごとの編集（全文の置き換えにしない）のトランザクションで返す（FR-175）
//   - 本体の操作 format（gq{モーション} と :format）の提供者になる（FR-176）。範囲を整形ツールに渡す手段は持たず、
//     全体を整形してから、範囲と重なる差分の塊だけを適用する（conform.nvim の、範囲の整形に対応しない整形ツールの扱いと同じ）
//
// 整形ツールには、バッファの本文を標準入力で渡し、標準出力を整形の結果として受け取る。
// 整形ツールが失敗したら（見つからない、終了コードが 0 でない、時間の上限を超えた、空の出力を返した）、編集を返さず、
// 保存は止めずに進め、標準エラー出力の要旨（error を含む最初の行か、無ければ最初の行。gist）を message/show で知らせる。
//
// # 起動の契機
//
// 宣言ファイルの起動の契機は、設定のファイルの種類の契機（settingsFileType。基本設計 6.7）の /formatters/*/fileTypes だけである。
// 本体は、設定の formatters に書いたファイルの種類のバッファを開いたときに、この拡張機能を起動する。
// 設定に何も書いていなければ起動せず、format の提供者の候補にもならない（ext.Manifest.HandlesFileType）。
//
// # 設定
//
// 拡張機能の ID は formatter である。本体の操作の名前 format と分け、:help format が操作の説明に当たるようにした。
// 設定は extensions.settings.formatter に書く（形は settings の doc）。例:
//
//	"formatter": {
//	  "formatters": [
//	    { "command": "ruff", "args": ["format", "--stdin-filename", "{path}", "-"], "fileTypes": ["python"] },
//	    { "command": "shfmt", "args": ["-filename", "{path}"], "fileTypes": ["sh", "bash"] }
//	  ]
//	}
//
// # LSP の拡張機能との関係
//
// 同じファイルの種類で LSP の拡張機能も format と saveHook を名乗るときの扱いは次のとおりである（基本設計 6.10）。
//
//   - format（gq）: 本体は、名乗った拡張機能のうち宣言ファイルを先に読んだものを使う（基本設計 5.5）。
//     同梱の拡張機能はディレクトリの名前の順に読むので、formatter（この拡張機能）が lsp より先になる。
//     設定の formatters に書いたファイルの種類では、整形ツールで整形し、書いていないファイルの種類では LSP で整形する
//   - 保存の前: 保存の流れは saveHook の拡張機能すべてに順に問い合わせるので、この拡張機能の後に LSP の拡張機能も整形する。
//     整形ツールだけを使うには、LSP の拡張機能の設定の言語サーバーに "formatOnSave": false を書く
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
		log.Fatalf("vi-imitation-ext-formatter: %v", err)
	}
}

// run は、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return newExtension().srv.Run(ctx)
}

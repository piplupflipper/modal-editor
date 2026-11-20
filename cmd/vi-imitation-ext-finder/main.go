// Command vi-imitation-ext-finder は、ファジーファインダーの拡張機能である（基本設計 6.9。FR-106、FR-107）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json の contributes.commands のコマンドが呼ばれたときに、子プロセスとして起動する。
// 本体とは標準入出力で JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
//
// コマンドは、入力の行と一覧を組にした部品（ext.ViewFinder）の中央のポップアップを popup/open で開く。
// 本体は入力の行が変わるたびに input/change で中身を送り、この拡張機能は絞り込んで並べた一覧を view/update で返す
// （絞り込みの計算は拡張機能が持つ。#177 の裁定）。一覧は後から届いてよく、FR-17 の期限の対象にしない。
// 選んだ（<CR>）項目は view/select で届き、ポップアップを閉じてから、項目の種類に合わせて editor/open などで開く。
//
// コマンドと既定のキーは、オーナーの nvim の fzf-lua の割り当てに合わせた（どれも仮）。<leader> は本体の設定の leader である。
//
//   - finder.files（<leader>ff）: 作業ディレクトリの下のファイル。fd（fdfind）か rg --files があれば使い、無ければ Go で歩く。
//     fzf-lua の <C-p> は割り当てない（本体の <C-p> は貼り直し。FR-55。#308 のオーナーの決めで、<leader> のキーを前提にする）
//   - finder.grep（<leader>fg）: 作業ディレクトリの下を rg で検索する。入力が変わるたびに rg を起動し直す（live grep）
//   - finder.grepDir（<leader>f.）: カーソルのあるバッファのファイルのディレクトリの下を rg で検索する
//   - finder.grepWord（<leader>fw）: カーソルの下の語を入力に入れて finder.grep を開く
//   - finder.buffers（<leader>fb）: タブ（editor/list の tabs。FR-107）
//   - finder.lines（<leader>fl）: カーソルのあるバッファの行
//   - finder.keys（<leader>fk）: キーの一覧（editor/list の keys）。選んだキーは editor/execute で本体に実行させる
//   - finder.commands（<leader>fc）: コマンド。選んだコマンドは editor/execute で本体に実行させる（:名前<CR> と同じ）
//   - finder.registers（<leader>fr）、finder.jumps（<leader>fj）: レジスターと履歴、ジャンプリスト
//   - finder.help（<leader>fh）: :help で引ける語（editor/list の help。FR-148）。選んだ語は editor/execute で本体に :help を開かせる
//   - finder.resume（<leader>fp）: 最後に開いたファインダーを、最後の入力で開き直す
//
// ファイル、検索、行、ジャンプリストの候補には、プレビューに出すもの（ext.ViewItem.Preview。FR-149）を付ける。
// ファイルは先頭から、検索と行とジャンプリストはその行を真ん中にして出す。中身は本体が読むので、この拡張機能は場所だけを渡す。
// キー、コマンド、レジスター、タブ、ヘルプには付けず、窓を一覧だけにする（#288 のオーナーの決め）。
// プレビューのスクロール（<S-Down> など。FR-159）は本体の操作で、この拡張機能は何もしない。
//
// キー、コマンド、ヘルプの語を実行させる editor/execute には、選んだ view/select の選択の番号（selection）を付ける。
// 本体は、その選択への応答で、利用者がノーマルモードで入力の途中でないときだけ実行する（ext.EditorExecuteParams）。
//
// マクロとドットリピートの再生中（command/execute の sync が true）に呼ばれたコマンドは、ポップアップを開いてから応答する。
// 本体は応答までに開いたポップアップへ再生の残りのキーを渡すので、記録したときと同じくファインダーの中で打ったことになる（PR #249 のレビューの必須 2）。
//
// 設定（initialize の settings）は次のとおりである（どれも省いてよい）。
//
//		{"hidden": true, "limit": 300}
//
//	  - hidden: ファイルと検索で隠しファイル（. で始まるもの）を含めるか。既定は true（オーナーの fzf-lua の設定と同じ）。.git の下はいつも除く
//	  - limit: 一覧に送る項目の数の上限。既定は 300。一致した数は見出しに出す
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
		log.Fatalf("vi-imitation-ext-finder: %v", err)
	}
}

// run は、本体から shutdown を受け取るか、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	f := newFinder(ctx)
	defer f.closeAll()
	return f.srv.Run(ctx)
}

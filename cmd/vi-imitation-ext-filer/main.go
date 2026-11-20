// Command vi-imitation-ext-filer は、ツリー型のファイラの拡張機能である（基本設計 6.9。FR-105、FR-147）。
//
// 本体（internal/exthost）が、宣言ファイル manifest.json の contributes.commands のコマンドが呼ばれたときに、子プロセスとして起動する。
// 本体とは標準入出力で JSON-RPC のメッセージをやり取りするので、ログは標準エラー出力へ書く。
//
// コマンドは、操作中の client の作業ディレクトリ（editor/state の workDir）を根にしたディレクトリの木を、木の部品（ext.ViewTree）で
// pane/open に渡して出す。出す場所は pane/open の placement で選ぶ（FR-147）。どちらも、開いた時点でカーソルのあるファイルの場所まで
// 木を開いて、そのファイルの項目にカーソルを置く（ext.ViewComponent.Cursor）。コマンドと既定のキーは、オーナーの nvim の neo-tree の
// 割り当てに合わせた（#286。どれも仮）。
//
//   - filer.popup（<leader>e）: 中央のポップアップに出し、ファイルを選んだら閉じる（closeOnSelect）。開いていればもう一度で閉じる
//   - filer.pane（<leader>E）: 端末多重化ソフトで分割したペインに出し、出しっぱなしにする。開いていればもう一度で閉じる。
//     元の client のタブが替わるたびに（view/tabChange）、そのファイルまで木を開いてカーソルを置く（neo-tree の follow_current_file）。
//     端末多重化ソフトの外では、本体がペインに代えて中央のポップアップに出す（FR-123）ので、選んだら閉じる
//
// 木の中のキーは、宣言ファイルの contributes.popupKeys で足す（本体のキーより先に引く）。
//
//   - l: action が select なので、本体が <CR> と同じく扱う。ディレクトリなら開閉し（view/expand）、ファイルなら選ぶ（view/select）
//   - h: 開いたディレクトリなら閉じ、それ以外なら親のディレクトリを閉じてそこへカーソルを移す（neo-tree の close_node）
//   - H: gitignore されたものを見せるかを切り替える
//   - <CR>: 本体の木の <CR>。l と同じである
//
// 選んだファイルは editor/open（focus が true）で開く。本体は、ファイラを開いた元の client のタブに開き、中央のポップアップなら閉じ、
// 分割したペインならペインを残して端末多重化ソフトのフォーカスを元の client のペインへ戻す（基本設計 1.2）。
// 絞り込みの入力は持たない。木の中身は本体のバッファなので、/ と n の検索がそのまま効く（FR-71）。
//
// 木の項目には、プレビューに出すもの（ext.ViewItem.Preview。FR-149）としてその絶対パスを付ける。本体は、窓の幅が設定の境目を超えると
// 窓の右に、ファイルなら先頭からの中身を、ディレクトリなら中身の一覧を出す。中身は本体が読むので、この拡張機能は項目ごとにファイルを読まない。
// 分割したペインは横に狭いので、境目の規則でふつうは出ない（出す場所では分けない。#288 の決め）。
//
// 見せるものは、オーナーの neo-tree の設定に合わせ、隠しファイル（. で始まるもの）は見せ、gitignore されたものは隠す。
// gitignore の判定は、ディレクトリを読むたびに、その子の名前を git check-ignore に渡して行う。git のリポジトリの外と、git が無いときは、
// 隠すものは無い。並びはディレクトリが先で、その中はバイトの順である（neo-tree の既定の sort_items と同じ）。
//
// ディレクトリの子は、開いたときに読む（ext.ViewItem.HasChildren と view/expand）。ディレクトリの読み込みと git の待ちは、
// ext/server のメッセージを処理する goroutine の上では行わず、開いた木ごとの操作の列の goroutine（tree.ops）で行う。
// 選んだファイルの editor/open は、読み込みの後ろで待たせず、別の列（tree.opens）から送る。本体が端末多重化ソフトのフォーカスを
// 元の client へ戻す期限（1 秒）を、遅い読み込みや git の待ちで過ぎないためである（#292）。
// 大きなディレクトリ（node_modules など）を開いても、ping の応答を遅らせないためである（dev-cycle 2.2）。
// git の子プロセスは、拡張機能が SIGKILL で終わっても残らないように起動する（procattr_linux.go）。
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
		log.Fatalf("vi-imitation-ext-filer: %v", err)
	}
}

// run は、本体から shutdown を受け取るか、本体が標準入力を閉じるか、シグナルを受け取るまで、メッセージを処理する。
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	f := newFiler(ctx)
	defer f.closeAll()
	return f.srv.Run(ctx)
}

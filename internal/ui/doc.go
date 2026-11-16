// Package ui は、編集の状態と表示のデータから、画面の内容をセルの格子（Grid）として作る（基本設計 2 章）。
//
// 入力は Frame で、internal/editor が internal/command の状態と、拡張機能から受け取った表示のデータ
// （構文の色、診断、行の印、出力の欄。基本設計 6.1、6.7）から詰める。ui は internal/command と同じ層なので、その型を import しない。
// 描画は Frame を読むだけで、バッファもカーソルも変更しない。
// 1 つの行の印の優先順位と、出力の欄の置き方（画面の下の分割）は、このパッケージが決める（panes.go）。
//
// 端末へ出す部分は、端末の制御のライブラリの裁定（#9）の後に作る。
// その部分は、前回の Grid と今回の Grid のセルを比べ、変わったセルだけを書けばよい。
//
// 表示幅の数え方（全角文字、タブ、制御文字、結合文字、不正な UTF-8）は Vim 9.1 の既定の設定に合わせる。
// 同じ数え方を、internal/command の望みの列に差し込む Columns として提供する。
//
// 全角の文字の表（widetable.go）と、その検査に使う Vim の値（testdata/vim-width.txt）は、
// このディレクトリで `go generate` を打つと、PATH にある vim から作り直す（internal/widthgen）。
// Vim か Go の Unicode の版を上げたときに打ち直し、`go test ./internal/ui` で確かめる。
package ui

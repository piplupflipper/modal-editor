// Package input は、キーの列を編集コマンドの値に解釈する（基本設計 3 章）。
//
// キー（editcmd.Key）と値（editcmd.Command）の型は、値を実行する internal/command と共有するため、土台の層の internal/editcmd に置く。
// このパッケージは、キーの列から操作を引く表（Keymap）と、キーを 1 個ずつ受け取って値を組み立てる状態機械（Parser）を持つ。
//
// 端末のバイト列からキーへの解読は、端末ライブラリの裁定（issue #9）を待つため、まだ持たない。
// このパッケージはバッファを変更しない。解釈した値の実行は internal/command が受け持つ。
package input

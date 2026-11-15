// Package server は、拡張機能の作者が使う補助である（基本設計 2 章の ext の「拡張機能の側で使う補助の関数」）。
//
// 本体との JSON-RPC のメッセージループ、バッファの複製の管理、ハンドラーの登録を受け持つ。
// 拡張機能の作者が書くのは、モーション、テキストオブジェクト、オペレーター、コマンドのハンドラーだけである。
// initialize、ping、$/cancel、buffer/open、buffer/change、buffer/close は Server が処理する。
//
// ext は土台の層で他のパッケージを import しないので、補助は internal/rpc を使うこのサブパッケージに置く。
// 公開する API には internal の下の型を出さない。
//
// 最小の拡張機能は、Server を作り、ハンドラーを登録し、main から Run を呼ぶ。
// 例は Example にある。
package server

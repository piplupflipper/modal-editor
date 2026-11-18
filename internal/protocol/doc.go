// Package protocol は、server と client の間でやり取りするメッセージと、その接続の口である（基本設計 1.2。FR-58）。
//
// # メッセージ
//
// client から server へは ClientMessage（Hello、Event）を送る。
// 接続の最初のメッセージは Hello で、client の種類と端末の大きさを知らせる。server は、最初が Hello でない接続を受け付けない。
// その後の Event は、キー、貼り付け、端末の大きさの変化である。
// server から client へは ServerMessage（Draw、SetClipboard）を送る。
// server が接続を閉じたら、client は端末を戻して終わる。
// client が接続を閉じたら（端末の入力が閉じた）、server はその client の端末が失われたとして扱う。
//
// どの client から届いたかは、メッセージに書かない。
// server が接続ごとに識別子を割り当て、イベントループへ渡すときに付ける（基本設計 2.1）。
//
// # 接続
//
// 接続の両端は ClientConn と ServerConn の口で表す。実装は 2 つある。
//
//   - Pipe: 1 つのプロセスの中でチャネルでつなぐ（FR-60 の構成）。メッセージを写さずにそのまま渡す
//   - NewServerSocket、NewClientSocket、Dial: server のソケット（Unix ドメインソケット）の上の JSON-RPC 2.0 の通知でつなぐ。
//     区切りは拡張機能との接続（基本設計 5.3）と同じ Content-Length のヘッダーで、internal/rpc の ReadMessage と WriteMessage を使う
//
// server と client の側はこの口だけを使うので、どちらの接続でも同じに動く。
//
// # 画面の送る単位
//
// 画面の内容を送る単位は、internal/ui の格子の差分である（screen.go）。
// Draw はどちらの接続でも ui.Screen を持ち、ソケットの接続だけが、送るときに前回の格子との差分にし、受けるときに格子に戻す。
//
// # 層
//
// このパッケージは internal/editcmd（キーの型）、internal/ui（格子の型）、internal/rpc（メッセージの区切り）を import するので、
// internal/editor と internal/term と同じ層に置く。
// internal/server と internal/client はその上の層に置き、どちらもこのパッケージを import する。
package protocol

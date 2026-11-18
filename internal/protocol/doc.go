// Package protocol は、server と client の間でやり取りするメッセージと、その接続の口である（基本設計 1.2。FR-58 の 1 段目）。
//
// # メッセージ
//
// client から server へは Event（キー、貼り付け、端末の大きさ）を送る。
// 接続の最初のメッセージは、端末の大きさを知らせる Event（Resize が true）にする。
// server から client へは ServerMessage（Draw、SetClipboard）を送る。
// server が接続を閉じたら、client は端末を戻して終わる。
// client が接続を閉じたら（端末の入力が閉じた）、server はその client の端末が失われたとして扱う。
//
// どの client から届いたかは、メッセージに書かない。
// server が接続ごとに識別子を割り当て、イベントループへ渡すときに付ける（基本設計 2.1）。
//
// # 接続
//
// 接続の両端は ClientConn と ServerConn の口で表す。
// 段階 3 の 1 段目では、1 つのプロセスの中でチャネルでつなぐ Pipe だけを持つ。
// 2 段目でソケットと JSON-RPC の実装を足すときも、server と client の側はこの口だけを使うので変えない。
//
// # 画面の送る単位
//
// 画面の内容をどの単位で送るか（internal/ui の格子の差分か、表示のデータか）は、基本設計 10 章の未決の項目である。
// 送る単位に関わる処理は screen.go の ScreenEncoder（server の側）と ScreenDecoder（client の側）に寄せ、
// 単位を差し替えるときはこの 2 つと Draw の中身だけを変える。
// 今は、server が組み立てた ui.Screen をそのまま渡し、client の internal/term が前回の格子との差分を出力する。
//
// # 層
//
// このパッケージは internal/editcmd（キーの型）と internal/ui（格子の型）を import するので、
// internal/editor と internal/term と同じ層に置く。
// internal/server と internal/client はその上の層に置き、どちらもこのパッケージを import する。
package protocol

// Package term は、実物の端末を制御する（#9 の 1 の裁定で、golang.org/x/term と ANSI の制御シーケンスで自前に作ると決まった）。
//
// 次の 3 つの部分から成る。
//
//   - Decoder: 端末から読んだバイト列を、キー（editcmd.Key）と貼り付けたテキスト（bracketed paste）に解読する。端末を使わずに試せる
//   - Painter: ui の格子（ui.Screen）を、前回の格子と比べて変わったセルだけを書く制御シーケンスにする。端末を使わずに試せる
//   - Terminal: tty を raw モードにし、代替画面と bracketed paste を有効にして、Decoder と Painter で読み書きする。
//     終わるときに、端末を元の状態に戻す
//
// # 層
//
// このパッケージは internal/editcmd（キーの型）と internal/ui（格子の型）を import する。
// internal/ui と同じ層には置けないので、internal/editor と同じ層に置き、internal/editor は import しない。
// Terminal を使うのは編集の client（internal/client）で、イベントを server へ送り、server から届いた画面を Draw で出す（基本設計 1.2）。
// Event は、client から server へ送る protocol.Event と同じ形の構造体なので、型の変換だけで渡せる。
//
// # 端末の前提
//
// 出力は xterm の制御シーケンス（ctlseqs）の範囲で書く。実際の端末で確かめたのは、2026-11-16 時点で tmux 3.4 の中だけである。
// terminfo は読まない。terminfo の無い環境（コンテナ）でも、同じ出力で動かすためである。
// 色は 24 ビット色をそのまま出す。24 ビット色を解釈しない端末への近似（256 色への丸め）はしない。
//
// 入力は、xterm、VTE、tmux の既定の設定で送られる列を解読する。
// kitty のキーボードのプロトコルと xterm の modifyOtherKeys の列（CSI u と CSI 27 ~）も、端末が送ってくれば解読するが、
// このパッケージから有効にはしない。
package term

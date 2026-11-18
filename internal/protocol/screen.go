package protocol

import "github.com/piplupflipper/modal-editor/internal/ui"

// このファイルは、画面の内容を送る単位を決める（基本設計 1.2 と 10 章の未決の項目）。
//
// この箇所だけで差し替えられるのは、「格子の全体」と「格子の差分」の間だけである。
// 10 章のもう一方の案（表示のデータを送り、client で格子に描く）を測るには、
// 格子への描画（internal/editor の render が呼ぶ ui.Renderer.Render）を境目より後ろ（client の側）へ移す必要がある。
// 今は描画がイベントループの中で済んでから editor.Terminal.Draw に ui.Screen が渡るので、server の側には表示のデータが残っていない。
// 境目の移動は 2 段目の issue で扱う。
//
// 今の単位は「internal/ui の格子の全体（ui.Screen）」である。
// server は組み立てた ui.Screen をそのまま Draw に入れ、client はそれを internal/term の Painter に渡して、前回の格子との差分を出力する。
// 1 つのプロセスの中では格子を写さずに渡せる（ui.Renderer.Render は描くたびに新しい格子を作り、渡した後に変えない）。
// ソケットに載せる 2 段目では、ScreenEncoder が前回の格子を覚えて変わった行だけを Draw に入れ、
// ScreenDecoder が手元の格子に当てて ui.Screen に戻す形にできる。

// Draw は、client の画面に出す内容である。
type Draw struct {
	// Screen は、画面の全体の内容とカーソルである。受け取った側は中身を変えない。
	Screen ui.Screen
}

func (Draw) serverMessage() {}

// ScreenEncoder は、server の側で、組み立てた画面を Draw にする。client との接続ごとに 1 つ持つ。
// ゼロ値で使える。
type ScreenEncoder struct{}

// Encode は、画面 s を送る Draw にする。s は、この後に変えないものを渡す。
func (*ScreenEncoder) Encode(s ui.Screen) Draw {
	return Draw{Screen: s}
}

// ScreenDecoder は、client の側で、届いた Draw を端末に出す画面に戻す。接続ごとに 1 つ持つ。
// ゼロ値で使える。
type ScreenDecoder struct{}

// Decode は、届いた d を、端末に出す画面にする。
func (*ScreenDecoder) Decode(d Draw) ui.Screen {
	return d.Screen
}

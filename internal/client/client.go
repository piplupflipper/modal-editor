// Package client は、端末を持つ側（編集の client）である（基本設計 1.2、2 章。FR-58 の 1 段目）。
//
// client は端末（internal/term）を持ち、端末から解読したキーを server へ送り、server から届いた画面を端末へ出す。
// 編集の状態は持たない。キーの列の解釈、マクロ、ドットリピートは server の側にある（基本設計 1.2）。
// ESC の単独と ESC で始まる列の区別（キーの解読）は、読み取りの時間に依存するので、端末を持つこちらに残す。
//
// # 層
//
// internal/term と internal/protocol を import するので、その上の層に置く。
// internal/server と同じ層で、互いに import しない。両者をつなぐのは cmd/modal-editor である。
package client

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/term"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// Terminal は、client が使う端末の口である。*term.Terminal がこれを満たす。テストでは偽物を差し込む。
type Terminal interface {
	// Input は、端末から解読したイベントを届いた順に送るチャネルを返す。端末の入力が終わると閉じる。
	Input() <-chan term.Event
	// Size は、端末の現在の列数と行数を返す。
	Size() (width, height int)
	// Draw は、画面 s を端末へ出す。
	Draw(s ui.Screen) error
	// SetClipboard は、text をホストのクリップボードへ送る（FR-56）。
	SetClipboard(text string) error
	// Restore は、端末を元に戻す。何度呼んでもよい。
	Restore() error
}

// Run は、tm を conn の先の server に編集の client として繋ぎ、server が接続を閉じるか、端末の入力が終わるまで動かす。
//
// 最初に端末の大きさを server へ知らせ（protocol の接続の最初のメッセージ）、その後は次の 2 つを並行に行う。
//
//   - 端末のイベントを server へ送る（別の goroutine）。端末の入力が終わったら接続を閉じ、server に端末が失われたことを知らせる
//   - server から届いた画面とクリップボードへの書き込みを、届いた順に端末へ出す（Run を呼んだ goroutine）
//
// 送る向きと受ける向きを別の goroutine にするのは、server が画面を送り終えるのを待つ間も、キーを送れるようにするためである。
//
// 接続が閉じたら、端末を戻し、接続を閉じて戻る。戻す誤りを返す。
// 端末への出力の誤りは logger に記録し、続ける（editor.Terminal の Draw と同じ扱い）。
// 送る goroutine は、端末の入力（Terminal.Input）が閉じるまで残る。呼び出し側は、Run の後に端末の入力を止める（term.Terminal.Close）。
// 送る goroutine が panic したら、端末を戻してから panic を続ける（基本設計 8 章）。
func Run(tm Terminal, conn protocol.ClientConn, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	w, h := tm.Size()
	if err := conn.Send(protocol.Event{Resize: true, Width: w, Height: h}); err == nil {
		go forward(tm, conn)
	}
	var dec protocol.ScreenDecoder
	for {
		m, err := conn.Recv()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, protocol.ErrClosed) {
				logger.Error("server からの受信に失敗", "err", err)
			}
			break
		}
		switch m := m.(type) {
		case protocol.Draw:
			if err := tm.Draw(dec.Decode(m)); err != nil {
				logger.Error("描画に失敗", "err", err)
			}
		case protocol.SetClipboard:
			if err := tm.SetClipboard(m.Text); err != nil {
				logger.Error("クリップボードへ送れない", "err", err)
			}
		default:
			logger.Error("知らないメッセージ", "type", fmt.Sprintf("%T", m))
		}
	}
	rerr := tm.Restore()
	_ = conn.Close() // 閉じる誤りは、端末を戻した後には扱えない
	if rerr != nil {
		return fmt.Errorf("client: %w", rerr)
	}
	return nil
}

// forward は、端末のイベントを server へ送る。端末の入力が終わったら接続を閉じる。
// server が接続を閉じた後も、端末の入力が閉じるまで読み捨てる（端末の読み取りを止めないため）。
func forward(tm Terminal, conn protocol.ClientConn) {
	defer func() { _ = conn.Close() }() // 閉じる誤りは、server の側の Recv で分かる
	defer func() {
		if p := recover(); p != nil {
			_ = tm.Restore() // panic を続けるので、戻す誤りは捨てる
			panic(p)
		}
	}()
	for ev := range tm.Input() {
		_ = conn.Send(protocol.Event(ev)) // 閉じた後の誤りは捨てる。Run が端末を戻して終わる
	}
}

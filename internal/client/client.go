// Package client は、端末を持つ側（編集の client）である（基本設計 1.2、2 章。FR-58）。
//
// client は端末（internal/term）を持ち、端末から解読したキーを server へ送り、server から届いた画面を端末へ出す。
// 編集の状態は持たない。キーの列の解釈、マクロ、ドットリピートは server の側にある（基本設計 1.2）。
// ESC の単独と ESC で始まる列の区別（キーの解読）は、読み取りの時間に依存するので、端末を持つこちらに残す。
//
// # 層
//
// internal/term と internal/protocol を import するので、その上の層に置く。
// internal/server と同じ層で、互いに import しない。両者をつなぐのは cmd/vi-imitation である。
package client

import (
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/piplupflipper/vi-imitation/internal/protocol"
	"github.com/piplupflipper/vi-imitation/internal/term"
	"github.com/piplupflipper/vi-imitation/internal/ui"
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
// 最初に、編集の client であることと端末の大きさを Hello で server へ知らせ（protocol の接続の最初のメッセージ）、その後は次の 2 つを並行に行う。
// conn は、1 つのプロセスの中の接続（protocol.Pipe）でも、server のソケットの接続（protocol.Dial）でもよい。
//
//   - 端末のイベントを server へ送る（別の goroutine）。端末の入力が終わったら接続を閉じ、server に端末が失われたことを知らせる
//   - server から届いた画面とクリップボードへの書き込みを、届いた順に端末へ出す（Run を呼んだ goroutine）
//
// 送る向きと受ける向きを別の goroutine にするのは、server が画面を送り終えるのを待つ間も、キーを送れるようにするためである。
//
// 接続が閉じたら、端末を戻し、接続を閉じて戻る。戻す誤りを返す。
// 端末を戻せたときは、server の終わり方を返す。終わりの知らせ（protocol.Exit）を理由なしで受けたか、自分の側で閉じたなら nil、
// 理由付きで受けたなら *ServerExitError、知らせを受けずに接続が閉じたなら ErrServerLost である。
// 端末への出力の誤りは logger に記録し、続ける（editor.Terminal の Draw と同じ扱い）。
// 送る goroutine は、端末の入力（Terminal.Input）が閉じるまで残る。呼び出し側は、Run の後に端末の入力を止める（term.Terminal.Close）。
// 送る goroutine が panic したら、端末を戻してから panic を続ける（基本設計 8 章）。
func Run(tm Terminal, conn protocol.ClientConn, logger *slog.Logger) error {
	w, h := tm.Size()
	sent := conn.Send(protocol.Hello{Kind: protocol.KindEdit, Width: w, Height: h, Features: Features(tm)}) == nil
	return run(tm, conn, logger, sent, nil)
}

// RunComponent は、tm を conn の先の server に、部品 view を描く部品の client（kind は protocol.KindPopup か KindPane）として繋ぎ、
// Run と同じく、server が接続を閉じるか、端末の入力が終わるまで動かす（基本設計 1.2。FR-67、FR-115）。
// 端末の受け渡し（protocol.FeatureTerminal）は宣言しない（server は部品の client に :! を頼まない）。
// server が部品を閉じたら理由の無い終わりの知らせが届き、nil を返す。呼び出し側はそのまま終わり、端末多重化ソフトのポップアップかペインが閉じる。
// pane は、部品の client のいる端末多重化ソフトのペインの識別子（TMUX_PANE、HERDR_PANE_ID。知らなければ空）で、Hello の Pane に載せる。
func RunComponent(tm Terminal, conn protocol.ClientConn, kind protocol.ClientKind, view uint64, pane string, logger *slog.Logger) error {
	w, h := tm.Size()
	sent := conn.Send(protocol.Hello{Kind: kind, Width: w, Height: h, View: view, Pane: pane}) == nil
	return run(tm, conn, logger, sent, nil)
}

// Start は、conn へ hello を送り、server から最初に届いたメッセージを返す。端末には何も出さない。
// 呼び出し側は、最初のメッセージを見て、編集の client として続けるか（Continue）、接続を閉じて別の形で動くかを決める
// （server が理由 server.BusyReason の終わりの知らせで断ったときに、1 つのプロセスで動かす。基本設計 1.2 の FR-62 までの扱い）。
// 送れないか、最初のメッセージの前に接続が閉じたら誤りを返す。
//
// hello を送れなくても、server が先に送った終わりの知らせ（Hello を読まずに断るとき）を読むために、最初のメッセージを読みにいく。
func Start(conn protocol.ClientConn, hello protocol.Hello) (protocol.ServerMessage, error) {
	serr := conn.Send(hello)
	m, err := conn.Recv()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrServerLost, errors.Join(serr, err))
	}
	if _, exit := m.(protocol.Exit); serr != nil && !exit {
		return nil, fmt.Errorf("client: %w", serr)
	}
	return m, nil
}

// Continue は、Start で hello を送って最初のメッセージ first を受けた conn で、Run と同じく編集の client として動く。
// first を、届いた最初のメッセージとして端末に出してから続ける。戻り値は Run と同じである。
func Continue(tm Terminal, conn protocol.ClientConn, first protocol.ServerMessage, logger *slog.Logger) error {
	return run(tm, conn, logger, true, first)
}

// run は、Run と Continue の本体である。sent は Hello を送れたか、first は Start で受けた最初のメッセージ（無ければ nil）である。
func run(tm Terminal, conn protocol.ClientConn, logger *slog.Logger, sent bool, first protocol.ServerMessage) error {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	h := newHandover(tm, conn, logger)
	if sent {
		go forward(tm, conn, h)
	}
	// endErr は、server の終わり方による Run の戻り値である（端末を戻した後に返す）。
	var endErr error
	exited := false
recv:
	for {
		m, err := first, error(nil)
		first = nil
		if m == nil {
			m, err = conn.Recv()
		}
		if err != nil {
			switch {
			case errors.Is(err, protocol.ErrClosed):
				// 自分の側で閉じた（端末の入力が終わった、呼び出し側が閉じた）
			case exited:
			case errors.Is(err, io.EOF):
				endErr = ErrServerLost
			default:
				logger.Error("server からの受信に失敗", "err", err)
				endErr = fmt.Errorf("%w: %w", ErrServerLost, err)
			}
			break recv
		}
		switch m := m.(type) {
		case protocol.Exit:
			exited = true
			if m.Reason != "" {
				endErr = &ServerExitError{Reason: m.Reason}
			}
		case protocol.Draw:
			if err := tm.Draw(m.Screen); err != nil {
				logger.Error("描画に失敗", "err", err)
			}
		case protocol.SetClipboard:
			if err := tm.SetClipboard(m.Text); err != nil {
				logger.Error("クリップボードへ送れない", "err", err)
			}
		case protocol.TerminalRun:
			h.start(m)
		case protocol.TerminalReclaim:
			h.reclaim(m.ID)
		case protocol.TerminalRelease:
			h.release(m.ID)
		default:
			logger.Error("知らないメッセージ", "type", fmt.Sprintf("%T", m))
		}
	}
	// 端末を渡したまま server との接続が切れたら、子プロセス（:!sh など）が終わるのを待ってから端末を戻す。
	// 待たずに戻ると、子プロセスが端末を使っている間に本体が終わり、端末の設定が混ざる。
	h.finish()
	rerr := tm.Restore()
	_ = conn.Close() // 閉じる誤りは、端末を戻した後には扱えない
	if rerr != nil {
		return fmt.Errorf("client: %w", rerr)
	}
	return endErr
}

// ErrServerLost は、server が終わりの知らせ（protocol.Exit）を送らずに接続が閉じたこと、つまり server が異常終了したことを表す。
// 未保存の変更を失ったかもしれないことを利用者が気づけるよう、呼び出し側は端末を戻した後に知らせて、0 でない終了コードで終わる。
var ErrServerLost = errors.New("server との接続が切れた（server が異常終了したかもしれない）")

// ServerExitError は、server が理由を添えて終わったこと（シグナルで止められたなど）を表す。
type ServerExitError struct {
	Reason string
}

func (e *ServerExitError) Error() string { return e.Reason }

// forward は、端末のイベントを server へ送る。端末の入力が終わったら接続を閉じる。
// server が接続を閉じた後も、端末の入力が閉じるまで読み捨てる（端末の読み取りを止めないため）。
// 「続けるには ENTER」の問いの間に打ったキーは、問いの答えにする（handover.answer）。
func forward(tm Terminal, conn protocol.ClientConn, h *handover) {
	defer func() { _ = conn.Close() }() // 閉じる誤りは、server の側の Recv で分かる
	defer func() {
		if p := recover(); p != nil {
			_ = tm.Restore() // panic を続けるので、戻す誤りは捨てる
			panic(p)
		}
	}()
	for ev := range tm.Input() {
		if h.answer(ev) {
			continue
		}
		_ = conn.Send(protocol.Event(ev)) // 閉じた後の誤りは捨てる。Run が端末を戻して終わる
	}
}

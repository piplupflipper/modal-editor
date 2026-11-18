package protocol

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// ErrClosed は、相手か自分が接続を閉じた後に Send を呼んだことを表す。
var ErrClosed = errors.New("protocol: connection closed")

// ClientMessage は、client から server へ送るメッセージである。Hello と Event のどちらかである。
type ClientMessage interface {
	clientMessage()
}

// ClientKind は、client の種類である（基本設計 1.2 の 4 種類）。
type ClientKind string

// client の種類。今の server が受け付けるのは KindEdit だけである。
const (
	// KindEdit は、編集の client（利用者がペインで起動した modal-editor）である。
	KindEdit ClientKind = "edit"
	// KindPopup は、ポップアップの部品の client である（FR-67。後の issue で動かす）。
	KindPopup ClientKind = "popup"
	// KindPane は、ペインの部品の client である（FR-115。後の issue で動かす）。
	KindPane ClientKind = "pane"
	// KindWait は、待つだけの client である（FR-83。後の issue で動かす）。端末を使わないので、大きさを持たない。
	KindWait ClientKind = "wait"
)

// Hello は、client が接続の最初に送るメッセージである。client の種類と端末の大きさを知らせる（基本設計 1.2）。
// server は、最初のメッセージが Hello でなければ、その接続を受け付けない。
// ペインの ID（FR-63）、部品の client の元の client（FR-67、FR-115）、待つだけの client の開くファイル（FR-83）は、
// それぞれを動かす issue でここに足す。
type Hello struct {
	Kind ClientKind
	// Width と Height は、端末の列数と行数である。端末を持たない client（KindWait）では 0 である。
	Width, Height int
}

func (Hello) clientMessage() {}

// Event は、client が端末から受けたイベント 1 件で、client から server へ送る。
// internal/term の Event と同じ形で、型の変換で作れる。
type Event struct {
	// Key は、押されたキーである。Resize が true か Paste が空でなければ読まない。
	Key editcmd.Key
	// Paste は、端末から貼り付けた（bracketed paste）テキストである。空でなければ、このイベントは貼り付けである。
	Paste string
	// Resize は、端末の大きさが Width と Height に変わったことを表す。
	Resize        bool
	Width, Height int
}

func (Event) clientMessage() {}

// ServerMessage は、server から client へ送るメッセージである。Draw と SetClipboard のどちらかである。
type ServerMessage interface {
	serverMessage()
}

// SetClipboard は、Text をホストのクリップボードへ送るよう client に頼む（FR-56。実物の端末では OSC 52）。
// クリップボードへ書く手段は端末を持つ client にしかないので、server から頼む。
type SetClipboard struct {
	Text string
}

func (SetClipboard) serverMessage() {}

// ClientConn は、client の側から見た接続である。どの goroutine から呼んでもよい。
type ClientConn interface {
	// Send は、m を server へ送る。どちらかが接続を閉じていれば ErrClosed を返す。
	Send(m ClientMessage) error
	// Recv は、server から届いたメッセージを、届いた順に 1 件返す。
	// server が接続を閉じ、届いたものを読み終えたら io.EOF を返す。自分が閉じた後は ErrClosed を返す。
	// Draw の格子は、次の Recv まで使える（ソケットの接続は、格子を使い回す。screen.go の screenDecoder）。
	Recv() (ServerMessage, error)
	// Close は、接続を閉じる。2 回目以降は何もしない。
	Close() error
}

// ServerConn は、server の側から見た、1 つの client との接続である。どの goroutine から呼んでもよい。
type ServerConn interface {
	// Send は、m を client へ送る。どちらかが接続を閉じていれば ErrClosed を返す。
	Send(m ServerMessage) error
	// Recv は、client から届いたメッセージを、届いた順に 1 件返す。
	// client が接続を閉じ、届いたものを読み終えたら io.EOF を返す。自分が閉じた後は ErrClosed を返す。
	Recv() (ClientMessage, error)
	// Close は、接続を閉じる。2 回目以降は何もしない。
	Close() error
}

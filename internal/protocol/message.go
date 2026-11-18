package protocol

import (
	"errors"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// ErrClosed は、相手か自分が接続を閉じた後に Send を呼んだことを表す。
var ErrClosed = errors.New("protocol: connection closed")

// Event は、client が端末から受けたイベント 1 件で、client から server へ送る。
// internal/term の Event と同じ形で、型の変換で作れる。
type Event struct {
	// Key は、押されたキーである。Resize が true か Paste が空でなければ読まない。
	Key editcmd.Key
	// Paste は、端末から貼り付けた（bracketed paste）テキストである。空でなければ、このイベントは貼り付けである。
	Paste string
	// Resize は、端末の大きさが Width と Height に変わったことを表す。接続の最初のイベントは、これで端末の大きさを知らせる。
	Resize        bool
	Width, Height int
}

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
	// Send は、ev を server へ送る。どちらかが接続を閉じていれば ErrClosed を返す。
	Send(ev Event) error
	// Recv は、server から届いたメッセージを、届いた順に 1 件返す。
	// server が接続を閉じ、届いたものを読み終えたら io.EOF を返す。自分が閉じた後は ErrClosed を返す。
	Recv() (ServerMessage, error)
	// Close は、接続を閉じる。2 回目以降は何もしない。
	Close() error
}

// ServerConn は、server の側から見た、1 つの client との接続である。どの goroutine から呼んでもよい。
type ServerConn interface {
	// Send は、m を client へ送る。どちらかが接続を閉じていれば ErrClosed を返す。
	Send(m ServerMessage) error
	// Recv は、client から届いたイベントを、届いた順に 1 件返す。
	// client が接続を閉じ、届いたものを読み終えたら io.EOF を返す。自分が閉じた後は ErrClosed を返す。
	Recv() (Event, error)
	// Close は、接続を閉じる。2 回目以降は何もしない。
	Close() error
}

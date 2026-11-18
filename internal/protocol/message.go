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

// MaxWidth、MaxHeight、MaxCells は、画面の格子の列数、行数、セルの数（列数 × 行数）の上限である。
// server は Hello と大きさの変化の Event の大きさで格子（ui.Grid）を作り、client は届いた画面の大きさで格子を作るので、
// 上限が無いと、壊れたか悪意のある相手が送った巨大な値でメモリを使い切り、プロセスごと落ちる（PR #200 のレビュー。
// Go の out of memory は recover できず、server では未保存の変更を失う）。
//
// 上限は、実際の端末で起こりうる大きさを断らないことを優先して決めた（仮）。
// server は端末の大きさを断らず、上限へ切り詰めて格子を作る（ClampSize）。切り詰めたときは、端末の右か下が空く。
//
//   - MaxCells: 1 セルが約 48 バイトなので、200 万セルで 1 つの格子が約 96 MB である。server と client はそれぞれ数個の格子を持つが、
//     16 GiB 級のマシンで落ちない大きさに収まる。8K の画面（7680 × 4320 ピクセル）を 6 × 12 ピクセルの小さな文字で埋めた
//     1280 列 360 行（約 46 万セル）や、横に並べた複数の画面にまたがる 4000 列 400 行（160 万セル）を切り詰めずに受け付ける
//   - MaxWidth と MaxHeight: 1 辺の上限で、細長い端末（2100 列 20 行など）を断らないよう、セルの数の上限とは別に大きく取った
const (
	MaxWidth  = 10000
	MaxHeight = 10000
	MaxCells  = 2_000_000
)

// ValidSize は、width と height が、どちらも 1 以上で上限以下で、セルの数が MaxCells 以下かを返す。
func ValidSize(width, height int) bool {
	return width >= 1 && width <= MaxWidth && height >= 1 && height <= MaxHeight && width*height <= MaxCells
}

// ClampSize は、1 以上の width と height を、ValidSize を満たす大きさへ切り詰める。
// 列数と行数をそれぞれ上限に切り詰め、セルの数が MaxCells を超えれば行数を減らす。
func ClampSize(width, height int) (int, int) {
	width, height = min(max(width, 1), MaxWidth), min(max(height, 1), MaxHeight)
	if width*height > MaxCells {
		height = MaxCells / width
	}
	return width, height
}

// Hello は、client が接続の最初に送るメッセージである。client の種類と端末の大きさを知らせる（基本設計 1.2）。
// server は、最初のメッセージが Hello でなければ、その接続を受け付けない。
// ペインの ID（FR-63）、部品の client の元の client（FR-67、FR-115）、待つだけの client の開くファイル（FR-83）は、
// それぞれを動かす issue でここに足す。
type Hello struct {
	Kind ClientKind
	// Width と Height は、端末の列数と行数である。端末を持たない client（KindWait）では 0 である。
	Width, Height int
	// Files は、編集の client の起動の引数のファイルの絶対パスである（#201）。
	// server は、この client を繋いだ後、イベントループを動かす前に、引数の順に開く（server を起動した client の引数ではなく、
	// 繋いだ client の引数を開くため。同じ window でほぼ同時に起動した 2 つの client のファイルを取り違えない）。
	Files []string
	// ReadOnly は、Files を編集を禁止した状態で開くことを表す（起動のオプション -R。FR-119）。
	ReadOnly bool
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

// ServerMessage は、server から client へ送るメッセージである。Draw、SetClipboard、Exit のどれかである。
type ServerMessage interface {
	serverMessage()
}

// SetClipboard は、Text をホストのクリップボードへ送るよう client に頼む（FR-56。実物の端末では OSC 52）。
// クリップボードへ書く手段は端末を持つ client にしかないので、server から頼む。
type SetClipboard struct {
	Text string
}

func (SetClipboard) serverMessage() {}

// Exit は、server が接続を閉じる前に送る、終わりの知らせである（PR #200 のレビューの推奨 2）。
// client は、Exit を受けずに接続が閉じたら、server が異常終了したとみなす。
type Exit struct {
	// Reason は、終わった理由である。空なら正常な終わり（:q など）で、空でなければ利用者に知らせる理由（シグナルなど）である。
	Reason string
}

func (Exit) serverMessage() {}

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

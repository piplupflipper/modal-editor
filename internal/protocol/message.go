package protocol

import (
	"errors"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// ErrClosed は、相手か自分が接続を閉じた後に Send を呼んだことを表す。
var ErrClosed = errors.New("protocol: connection closed")

// ClientMessage は、client から server へ送るメッセージである。Hello、Event、TerminalDone、TerminalReleased のどれかである。
type ClientMessage interface {
	clientMessage()
}

// ClientKind は、client の種類である（基本設計 1.2 の 4 種類）。
type ClientKind string

// client の種類。
const (
	// KindEdit は、編集の client（利用者がペインで起動した vi-imitation）である。
	KindEdit ClientKind = "edit"
	// KindPopup は、ポップアップの部品の client である（FR-67）。server が端末多重化ソフトの中央のポップアップの中で
	// vi-imitation -popup <部品の ID> として起動する。Hello の View に、描く部品の ID を載せる。
	KindPopup ClientKind = "popup"
	// KindPane は、ペインの部品の client である（FR-115）。server が端末多重化ソフトで分割したペインの中で
	// vi-imitation -pane <部品の ID> として起動する。Hello の View に、描く部品の ID を載せる。
	KindPane ClientKind = "pane"
	// KindWait は、待つだけの client である（FR-83）。端末を使わないので、大きさを持たない。
	// Hello の Files を開かせ、それらのタブがすべて閉じられたら、server から理由の無い Exit を受けて終わる。
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
	// Pane は、client のペインの識別子（tmux の TMUX_PANE、herdr の HERDR_PANE_ID）である。
	// server は、別の client のタブのファイルを開こうとした client から、そのタブの client のペインへフォーカスを移すのに使う（FR-63）。
	// 端末多重化ソフトの外では空である。
	Pane string
	// WorkDir は、client の作業ディレクトリである（#215。PR #207 の未確認事項）。
	// server は、この client で打った :e の相対パスと、外部のコマンドの作業ディレクトリに使う。空なら server の作業ディレクトリを使う。
	WorkDir string
	// Env は、client の環境変数のうち、server が外部のコマンドへ client ごとに渡すもの（"名前=値" の列。TMUX_PANE、
	// HERDR_PANE_ID、HERDR_TAB_ID）である。server のプロセスの環境は、server を起動した client のものなので、client ごとに上書きする。
	Env []string
	// Parent は、待つだけの client（KindWait）を起動したプロセスの祖先にある編集の client の識別子である（FR-83、FR-117）。
	// :! を実行した client が子プロセスの環境変数 VI_IMITATION_CLIENT に入れた値で、server は Files をこの client のタブに開く。
	// 0 なら知らない（:! の外で起動した）ので、server は操作中の client に開く。
	Parent uint64
	// Features は、client が扱える機能の名前の列である（#226。PR #234 のレビューの必須 1）。
	// 版の違う client は、知らないメソッドの通知を読み飛ばすので、応答の要るメッセージ（FeatureTerminal の terminal/run など）を
	// 読み飛ばされると、server は応答を待ち続ける。server は、宣言した機能のメッセージだけを送る。
	Features []string
	// View は、部品の client（KindPopup、KindPane）が描く部品の ID である（server の中で一意で、使い回さない。基本設計 1.2 の
	// id 付き通知の組の取り決めの ext.ViewID と同じ番号）。server は、起動の引数で渡した ID をそのまま返してもらい、
	// その部品を開いた元の編集の client を引く（元の client を部品の client に知らせる必要が無い）。
	// 知らない ID（部品が先に閉じた）なら、server は理由の無い Exit で断る。部品の client でなければ 0 である。
	View uint64
}

// FeatureTerminal は、端末の受け渡し（terminal/run、terminal/reclaim、terminal/release に応えること）を扱えることを表す Hello.Features の名前である。
const FeatureTerminal = "terminal"

// HasFeature は、h が機能 name を宣言しているかを返す。
func (h Hello) HasFeature(name string) bool {
	for _, f := range h.Features {
		if f == name {
			return true
		}
	}
	return false
}

func (Hello) clientMessage() {}

// TerminalDone は、TerminalRun の子プロセスがすべて終わったことを、client から server へ知らせる（FR-79、FR-80）。
// client は、端末を取り戻して代替画面に入り直してから送る。
type TerminalDone struct {
	// ID は、TerminalRun の ID である。
	ID uint64
	// Status は、最後に動かしたコマンドの終了コードである。0 でないコマンドがあれば、そこで止めてその値を入れる。
	Status int
	// Err は、コマンドを起動できなかったか、シグナルで終わったときの理由である。空なら Status が結果である。
	Err string
}

func (TerminalDone) clientMessage() {}

// TerminalReleased は、TerminalRelease を受けて端末を子プロセスへ渡し直したことを、client から server へ知らせる（FR-117）。
// server はこれを受けてから、待つだけの client を終わらせる（子プロセスが端末を使い始める前に、こちらが端末を手放すため）。
// 子プロセスが既に終わっていて渡し直さなかったときも送る。
type TerminalReleased struct {
	ID uint64
}

func (TerminalReleased) clientMessage() {}

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

// ServerMessage は、server から client へ送るメッセージである。Draw、SetClipboard、Exit、TerminalRun、TerminalReclaim、TerminalRelease のどれかである。
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

// TerminalRun は、端末を子プロセスへ渡して Commands を順に動かすよう、server から編集の client へ頼む（基本設計 4.7、4.9。FR-79、FR-80）。
// 端末を持つのは client なので、子プロセスを起動するのも client である。
// client は端末を通常の状態へ戻し、コマンドを 1 つずつ動かし、0 でない終了コードで終わったらそこで止める。
// 終わったら（Prompt なら「続けるには ENTER を押してください」を出してキーを待ってから）端末を取り戻し、TerminalDone を返す。
type TerminalRun struct {
	// ID は、この実行を指す番号である。server が決め、TerminalDone、TerminalReclaim、TerminalRelease で同じ値を使う。
	ID uint64
	// Commands は、順に動かすコマンドである。
	Commands []TerminalCommand
	// Env は、子プロセスの環境変数に、client の環境の後に足す "名前=値" の列である（FR-82 の VI_IMITATION_SOCKET、EDITOR、GIT_EDITOR と、
	// :! を実行した client の識別子 VI_IMITATION_CLIENT）。
	Env []string
	// Prompt は、終わった後に「続けるには ENTER を押してください」を出し、キーを 1 つ待ってから代替画面に戻ることを表す（:! の Vim の動作）。
	// 待つ間に打ったキーが <CR>、<Space>、<Esc> でなければ、そのキーを TerminalDone の後に server へ送る（Vim の hit-enter と同じ）。
	Prompt bool
}

func (TerminalRun) serverMessage() {}

// TerminalCommand は、TerminalRun で動かすコマンド 1 つである。
type TerminalCommand struct {
	// Shell が空でなければ、client のシェル（環境変数 SHELL、無ければ /bin/sh）に -c で渡すコマンドである（:!）。Argv は読まない。
	Shell string
	// Argv は、シェルを通さずに起動するコマンドと引数である（sudo tee <パス>。FR-79）。
	Argv []string
	// Input は、標準入力へ渡すテキストである（tee へ渡すバッファの中身）。PipeInput が false なら読まず、標準入力は端末である。
	Input     []byte
	PipeInput bool
	// DiscardOutput は、標準出力を捨てることを表す（tee の標準出力）。false なら標準出力は端末である。標準エラー出力はいつも端末である。
	DiscardOutput bool
}

// TerminalReclaim は、TerminalRun の子プロセスが動いている間に、client が端末を取り戻して画面を描くよう頼む（FR-117）。
// 子プロセスの子孫が起動した待つだけの client のファイルを、:! を実行した client のタブで編集させるためである。
// 子プロセスが既に終わっていれば、client は何もしない。
type TerminalReclaim struct {
	ID uint64
}

func (TerminalReclaim) serverMessage() {}

// TerminalRelease は、TerminalReclaim で取り戻した端末を、子プロセスへ渡し直すよう頼む（FR-117）。client は TerminalReleased を返す。
type TerminalRelease struct {
	ID uint64
}

func (TerminalRelease) serverMessage() {}

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

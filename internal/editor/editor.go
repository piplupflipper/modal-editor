package editor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command"
	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/editcmd"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/input"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 設定の既定値（仮）。
const (
	// DefaultSyncTimeout は、キー入力ごとに結果が要る問い合わせの期限の既定値である（FR-17）。
	// 既定値は #43 の裁定 1-a で 100 ms に決まった（段階 1 の計測からの提案値。PR #37）。
	DefaultSyncTimeout = 100 * time.Millisecond
	// DefaultStartSyncTimeout は、起動を含む問い合わせ（拡張機能が未起動か起動中のときの問い合わせ）の期限の既定値（仮）である（基本設計 5.2）。
	// #43 の裁定 1-b で、起動を含む問い合わせだけ別の長い期限を使うことに決まった。
	// 段階 1 の計測では、起動から最初の応答まで 10 万行で 43〜56 ms、100 万行で 411〜547 ms だったので、仮に 1 秒にした。
	DefaultStartSyncTimeout = time.Second
	// DefaultKeyTimeout は、曖昧なキーの列（利用者のキーマップの接頭辞、本体の y と拡張機能の ys）を確定させるまでの待ち時間である。
	// Vim の 'timeoutlen' の既定と同じ 1000 ms にした。
	DefaultKeyTimeout = time.Second
	// maxHistory は、診断と印をずらすために、バッファごとに覚えておく直近の変更の数である（基本設計 6.7。仮に 100）。
	maxHistory = 100
	// maxOutputLines は、出力の欄 1 つあたりに持つ行数の上限である（基本設計 6.7。仮に 10000）。
	maxOutputLines = 10000
	// maxOutputLineBytes は、出力の欄の 1 行のバイト数の上限である（基本設計 6.7。仮に 4096）。超えたら次の行へ送る（NFR-03）。
	maxOutputLineBytes = 4096
)

// interruptKey は、中断のキー（<C-c>）である。
// マクロの再生と拡張機能の応答の待ちを止める。それ以外のときは <Esc> として扱う。
// 端末の側は、このキーを特別に扱わずに TermEvent で送ればよい。
var interruptKey = editcmd.Key{Code: editcmd.CodeRune, Rune: 'c', Mod: editcmd.ModCtrl}

// ErrTerminalLost は、端末の入力（Terminal.Input）が閉じたので Run が終わったことを表す。
// :q で終わったときの nil と区別し、呼び出し側が未保存の変更の退避などを判断できるようにする。
var ErrTerminalLost = errors.New("editor: terminal input closed")

// Terminal は、端末の口である。端末の制御のライブラリ（#9 で裁定待ち）を包んだものを、呼び出し側が差し込む。
type Terminal interface {
	// Input は、キーとリサイズのイベントを届いた順に送るチャネルを返す。
	// 閉じると、Run は終わる（端末が失われた）。
	Input() <-chan TermEvent
	// Size は、端末の現在の列数と行数を返す。Run の始めに 1 回だけ呼ぶ。
	Size() (width, height int)
	// Draw は、画面の内容とカーソルを端末へ出す。前回との差分だけを書くのは実装の自由である。
	// Run の goroutine から呼ぶ。誤りはログに記録し、編集は続ける。
	// s の格子は描くたびに新しく作り、渡した後に変えない。実装は s を別の goroutine へ渡して後で出してもよい（internal/server）。
	Draw(s ui.Screen) error
	// SetClipboard は、text をホストのクリップボードへ送る（FR-56。実物の端末では OSC 52 の制御シーケンス）。
	// 端末の応答は待たない。Run の goroutine から呼ぶ。誤りはログに記録し、編集は続ける。
	SetClipboard(text string) error
}

// TermEvent は、端末からのイベント 1 件である。
type TermEvent struct {
	// Key は、押されたキーである。Resize が true か Paste が空でなければ読まない。
	Key editcmd.Key
	// Paste は、端末から貼り付けた（bracketed paste）テキストである。空でなければ、このイベントは貼り付けである。
	// 改行は "\n" にそろえ、"\n" と "\t" の他の制御文字を含まない形で渡す。
	// キーの列と違い、利用者のキーマップを通さず、ノーマルモードとビジュアルモードでもコマンドとして解釈しない（command.State.Paste）。
	Paste string
	// Resize は、端末の大きさが Width と Height に変わったことを表す。
	Resize        bool
	Width, Height int
	// Client は、このイベントを受けた client である（基本設計 2.1 の段階 3）。internal/server が接続ごとに割り当てる。
	// 今は編集の client が 1 つだけなので、イベントループは読まない。forwardInput がキーの列に積むときに捨て、大きさも 1 つに上書きする。
	// 2 段目以降の複数の client の issue で、キーの列、中断の印、キーの列の待ち、オペレーターの途中の状態を client ごとに分けるときに使う（基本設計 2.1、FR-62）。
	Client ClientID
	// Terminal は、端末を子プロセスへ渡した実行（TerminalHandover）の結果である。nil でなければ、このイベントはキーではない（handover.go）。
	// キーと同じ列で届けるので、結果より前に打ったキーは結果より前に、後に打ったキーは後に届く。
	Terminal *TerminalResult
}

// ClientID は、server に繋いだ client の識別子である。0 は「client を区別しない」（server を通さずに端末をつないだ場合）を表す。
type ClientID uint64

// Config は、エディタの設定である。ゼロ値の項目は既定値を使う。
// 設定ファイル（FR-40、FR-41）の読み込みは #9 の裁定の後に作り、読み込んだ値をこの形で渡す。
type Config struct {
	// Keymap は、利用者のキーマップ（基本設計 7 章の keymap）である。
	// モードの名前（"normal"、"visual"、"insert"、"commandLine"）から、キーの列の表記から展開するキーの列の表記への対応である。
	// 展開した列は、もう一度キーマップで展開しない（Vim の noremap と同じ）。
	// 拡張機能の起動を待たずに反映する（FR-41）。
	Keymap map[string]map[string]string
	// KeymapActions は、利用者のキーマップのうち、右辺に本体の操作の ID を書いた割り当て（{ "action": "<ID>" }。FR-126）である。
	// モードの名前（"normal"、"visual"、"insert"）から、キーの列の表記から操作の ID への対応である。
	// Keymap と違ってキーの列に展開せず、操作の表（input.Keymap）へ登録し、同じモードの同じキーの列の本体の既定の登録に勝つ
	// （applyKeymapActions）。使えない項目は捨て、起動後に設定の誤りとして知らせる。
	KeymapActions map[string]map[string]string
	// Leader は、キーの表記の <Leader> に当たるキーの列の表記である（基本設計 7.2 の leader。FR-97）。
	// 空なら editcmd.DefaultLeader（<Space>）を使う。Keymap と、拡張機能の宣言の keys の <Leader> をこの値に置き換える。
	Leader string
	// SyncTimeout は、キー入力ごとに結果が要る問い合わせの期限（基本設計 7 章の syncTimeoutMs）である。
	// nil なら DefaultSyncTimeout を使う。0 は「待たない」（FR-18）。
	SyncTimeout *time.Duration
	// StartSyncTimeout は、キー入力ごとに結果が要る問い合わせのうち、起動を含むもの（問い合わせた時点で拡張機能が未起動か起動中）の期限である（基本設計 5.2）。
	// 0 以下なら DefaultStartSyncTimeout を使う。SyncTimeout より短ければ SyncTimeout を使う。
	// SyncTimeout が 0（待たない）のときと、マクロとドットリピートの再生中は使わない。
	StartSyncTimeout time.Duration
	// ReplayTimeout は、マクロとドットリピートの再生中に、拡張機能の応答を待つ上限である（FR-19 の「応答が無ければ」）。
	// 0 以下なら exthost の Config.SendTimeout を使う。
	ReplayTimeout time.Duration
	// KeyTimeout は、曖昧なキーの列を確定させるまでの待ち時間である。0 以下なら DefaultKeyTimeout を使う。
	KeyTimeout time.Duration
	// FileTypes は、ファイルの種類の対応表に足す項目である（基本設計 6.7 のファイルの種類の判定）。
	// キーは拡張子（".md"）かファイル名（"Makefile"）、値はファイルの種類の名前である。既定の表より優先する。
	FileTypes map[string]string
	// Extensions は、拡張機能の定義（検査済みの宣言ファイル）の一覧である。宣言ファイルを探して読むのは呼び出し側である。
	Extensions []exthost.Spec
	// Host は、exthost.Host の設定である。
	Host exthost.Config
	// WorkDir は、相対パスを解決する作業ディレクトリである。空なら os.Getwd を使う。
	WorkDir string
	// MCPSocket は、すべての拡張機能へ環境変数 ext.EnvMCPSocket で渡す MCP のソケットのパスである（基本設計 6.2）。
	// 空なら、New が exthost.NewMCPSocket で起動ごとに一意なパスを決め、Close でそのディレクトリを消す。
	// 決められなければ、空の値を渡す（本体の環境の同じ名前の変数は引き継がない）。
	// 指定したパスは、呼び出し側が用意し、後始末する。
	// Host.SocketPath が空なら、このパスを入れる。起動の契機 socket の拡張機能があれば、本体はこのパスで起動時から待ち受け、
	// 待ち受けたソケットのファイルは Close で消す（exthost.Config.SocketPath）。
	MCPSocket string
	// Logger は、ログの記録先である。nil なら slog.Default() を使う。
	Logger *slog.Logger
	// OnPanic は、Editor が起こす goroutine（端末の入力の受け取り、色の表の作成）が panic したときに、
	// その goroutine の上で呼ぶ関数である。呼んだ後、同じ値で panic を続ける。nil なら何もしない。
	// Host.OnPanic が nil なら、exthost の goroutine にも同じ関数を使う。
	// 呼び出し側は、端末を元に戻す関数を渡す（基本設計 8 章、#114）。どの goroutine からも、何度呼ばれてもよい関数にする。
	// Run を呼ぶ goroutine（イベントループ）の panic は、Run の呼び出し側が recover で扱う。
	OnPanic func()
	// FixEndOfLine は、書き出すときに末尾の改行を補うかどうか（基本設計 4.1。Vim の 'fixendofline'）である。
	// 開くバッファすべてに buffer.Buffer.SetFixEndOfLine で設定する。nil なら既定の有効（true）である。
	FixEndOfLine *bool
	// ReadOnlyArgs は、Open で開くファイル（起動の引数のファイル）を、編集を禁止した状態で開くことを表す（起動のオプション -R。FR-119）。
	// :e などで後から開くファイルには効かない（基本設計 4.6）。
	ReadOnlyArgs bool
	// ReadOnlyPaths は、開いた時点で編集を禁止にするファイルの置き場所である（設定の readonlyPaths。FR-120）。
	// 各項目はディレクトリの絶対パスか、"/" を含まないディレクトリの名前（"node_modules"）で、照合の規則は underReadOnlyPath にある。
	ReadOnlyPaths []string
	// ColorScheme は、設定の colorscheme で選んだ配色の、色の名前から属性への対応（FR-125）である。
	// ui.DefaultTheme を名前ごとに置き換える（ui.Theme.WithColors）。nil なら配色を選んでいない。
	ColorScheme map[string]ui.Attr
	// Colors は、色の名前から属性への対応（設定の colors。FR-91）である。
	// ui.DefaultTheme に ColorScheme を重ねたものを、さらに名前ごとに置き換える（ui.Theme.WithColors）。
	Colors map[string]ui.Attr
	// Symbols は、記号の名前から描く文字列への対応（設定の symbols。FR-93）である。値は ui.CheckSymbol を通したものにする。
	Symbols ui.Symbols
	// StatusLine は、ステータス行に並べる部品の列（設定の statusline。FR-92）である。nil なら ui.DefaultStatusLine を使う。
	StatusLine *ui.StatusLine
	// TabLine は、タブの行を出す場合（設定の tabline.show。FR-62）である。ゼロ値は、タブが 1 つでも出す（ui.TabLineAlways。#215 のオーナーの裁定）。
	TabLine ui.TabLineMode
	// TabLineParts は、タブの行に並べる部品の列（設定の tabline の left と right。FR-92）である。nil なら ui.DefaultTabLineParts を使う。
	TabLineParts *ui.StatusLine
	// SplitPane は、:diffsplit（基本設計 6.8。FR-99）で、from のペインを分割し、新しいペインで file を開く編集の client を、
	// このエディタの server に繋ぐ形で起動する関数である（internal/mux の Mux.SplitPane を包んだもの）。dir は起動するディレクトリである。
	// file が空なら、ファイルを渡さずに起動する（拡張機能の diff/start。diffstart.go）。
	// 新しい client は、環境変数 EnvSplit に token を入れて起動する。client はそれを Hello の Env で知らせ、イベントループが待ちと照らし合わせる。
	// イベントループを止めないよう、別の goroutine で呼ぶ。nil なら :diffsplit は端末多重化ソフトの外として誤りにする。
	SplitPane func(from, file, dir, token string) error
	// FocusPane は、別の編集の client のタブのファイルを開こうとしたときに、from のペインから pane のペインへ
	// フォーカスを移す関数である（FR-63。internal/mux の Mux.FocusPane を包んだもの）。ペインは ClientInfo.Pane である。
	// イベントループを止めないよう、別の goroutine で呼ぶ。nil なら移さず、メッセージだけを出す。
	FocusPane func(from, pane string) error
	// PaneFocused は、ペイン pane が利用者の見ているアクティブなペインかを返す関数である（internal/mux の Mux.PaneFocused を包んだもの）。
	// ペインの部品で選んだ後に元の client のペインへフォーカスを戻す前に、利用者がまだ部品のペインにいるかを確かめる（component.go）。
	// 別の goroutine で呼ぶ。nil なら戻さない。
	PaneFocused func(pane string) (bool, error)
	// OpenPopup は、中央のポップアップ（基本設計 1.4。FR-67）を、端末多重化ソフトのポップアップとして開き、その中で部品 view を描く
	// ポップアップの部品の client を、このエディタの server に繋ぐ形で起動する関数である（internal/mux の Mux.OpenPopup を包んだもの。component.go）。
	// from は元の client のペイン、dir は起動するディレクトリである。部品の client が繋いだら、server が AttachComponent を呼ぶ。
	// イベントループを止めないよう、別の goroutine で呼ぶ。nil か、元の client のペインを知らないときは、編集の client の画面の中央に描く。
	// 誤りを返したら、誤りを元の client のメッセージの行に出し、編集の client の画面の中央に描く。
	OpenPopup func(from string, view uint64, dir string) error
	// OpenPane は、拡張機能の pane/open（FR-115）で、from のペインを分割し、新しいペインで部品 view を描くペインの部品の client を
	// 起動する関数である（internal/mux の Mux.SplitPane を包んだもの。component.go）。使い方は OpenPopup と同じである。
	// nil か、元の client のペインを知らないときは、ペインに代えて中央のポップアップに出す（FR-123）。
	OpenPane func(from string, view uint64, dir string, opts PaneOptions) error
	// Problems は、呼び出し側が設定ファイルと宣言ファイルを読んだときの誤り（位置と内容）である（基本設計 8 章）。
	// New は、すべてをログに記録し、New 自身が見つけた設定の誤りより先に並べて、最初の 1 件を起動後のメッセージで知らせる。
	Problems []error
	// CheckInterval は、開いているバッファのファイルが外で変わっていないかを確かめる間隔である（基本設計 6.7）。
	// 0 なら DefaultCheckInterval、負なら確かめない（:w の前の確認は、この値によらず行う）。
	CheckInterval time.Duration
	// AutoRead は、未変更のバッファのファイルが外で変わったときに、問わずに読み直すかどうか（Vim の 'autoread'）である。
	// nil なら既定の有効（true）である。Vim の既定は無効だが、エージェントがディスクへ直接書いた変更をすぐ画面へ出すために有効にした（Neovim の既定と同じ）。
	AutoRead *bool
	// Registers は、レジスターの履歴の設定（基本設計 7 章の registers。FR-52、FR-53）である。
	// ゼロ値は、すべての項目が既定値の設定である。すべてのバッファの command.State のレジスターに設定する。
	Registers command.RegisterConfig
	// NoClipboard は、レジスターの履歴の先頭をホストのクリップボードへ送らないことを表す（基本設計 7 章の registers.clipboard が "off"）。
	// ゼロ値の false は既定の "osc52" で、履歴の先頭が変わるたびに Terminal.SetClipboard で送る（FR-56）。
	NoClipboard bool
	// ChildEnv は、本体が起動する外部のコマンド（:{range}! と :r ! のシェル）の環境変数に、本体の環境の後に足す "名前=値" の列である。
	// 外部のコマンドはイベントループを止めずに動くので、子が起動した vi-imitation（FR-83）が server に繋いでも詰まらない。
	// 同じ名前は後のものが勝つ（os/exec の決まり）。
	// FR-82 の server のソケットのパス（仮に VI_IMITATION_SOCKET）と、EDITOR、GIT_EDITOR を入れる口で、
	// 値は server のソケットができてから呼び出し側が入れる（#190）。
	// :! のシェル（client が起動する。FR-80）と、権限を上げた保存のコマンド（FR-79）の環境変数にも、client の環境の後に足す。
	ChildEnv []string
	// ElevateCommand は、権限の不足で書けないファイルを書くときに、tee <パス> の前に付けるコマンドである（設定の elevateCommand。FR-79）。
	// 空なら DefaultElevateCommand（["sudo"]）を使う。
	ElevateCommand []string
	// ExtManager は、:ext sync（拡張機能の管理。基本設計 5.6）の設定である。ゼロ値は本体の既定の置き場所を使う（extsync.go）。
	ExtManager ExtManagerConfig
}

// Editor は、イベントループと、編集の状態の全体である。New で作り、Run で動かし、Close で終える。
// Run 以外のメソッド（Open）は、Run の前に、Run と同じ goroutine から呼ぶ。
//
// 編集の client ごとの画面の状態（viewFields）は、イベントループが扱っている client のものを Editor に埋め込んだ欄に置き、
// 他の client のものは clientView に退避する（clients.go。基本設計 1.2、4.4 の段階 3）。
type Editor struct {
	cfg    Config
	log    *slog.Logger
	host   *exthost.Host
	keymap *input.Keymap
	// exCommands は、コマンドラインから呼べる拡張機能のコマンドの ID から、宣言した拡張機能の ID への表である（buildExCommands）。
	exCommands map[string]string
	// leader は、<Leader> を置き換えたキーの列（Config.Leader を読んだもの。FR-97）である。キーの一覧（FR-90）で使う。
	leader   []editcmd.Key
	fileType map[string]string
	// fixEOL は、開くバッファに設定する末尾の改行の補い（Config.FixEndOfLine）である。
	fixEOL bool
	// popupKeys は、拡張機能の ID から、その拡張機能の contributes.popupKeys のキーへの表である（FR-69。popup.go）。
	popupKeys map[string][]popupKeyBinding
	// floatSeq は、最後に開いたポップアップの番号である（floatState.id）。
	floatSeq uint64
	// paneActive と paneSaved は、ペインの部品の client のキーを処理している間（paneInput）の、そのペインの部品と、
	// 退避した元の client のポップアップである（component.go）。処理していなければ nil である。
	paneActive, paneSaved *floatState
	// selectionSeq は、最後に拡張機能のポップアップで利用者が選んだときの選択の番号で、execGrants は、拡張機能ごとの、
	// まだ使っていない editor/execute の許し（list.go）である。
	selectionSeq uint64
	execGrants   map[string]execGrant
	// noRecord は、feedKey がマクロに記録せずに State へ渡す間であることを表す（キーの一覧で選んだキー。selectKeyRow）。
	// 選んだ <CR> がもう記録されているので、選んだキーまで記録すると、再生で 2 回動くためである。
	noRecord bool

	// mcpSocket は、New が決めた MCP のソケットのパスである。Config.MCPSocket を指定したときと、決められなかったときは nil。
	mcpSocket *exthost.MCPSocket

	// viewFields は、いま読み込んでいる client（view）の画面の状態である。
	viewFields
	// clients は、繋いでいる編集の client を、繋いだ順に並べたものである（clients.go）。
	clients []*clientView
	// view は、viewFields を読み込んでいる client である。描画は client ごとに読み込み直す（render）。
	view *clientView
	// op は、操作中の client（最後にキーを受けた編集の client。基本設計 1.2）である。拡張機能の editor/open などはこの client のタブに開く。
	op *clientView
	// shared は、レジスターなどの client の間で共有する状態（command.State.TakeOverShared）を最後に持った State である。
	// キーを打つ client が替わったら、その client のカーソルのあるバッファの State へ引き継ぐ（useClient）。
	shared *command.State
	// asker は、確認の問いか選択肢の問いを出している client である。問いが無ければ nil（syncAsker）。
	asker *clientView
	// extClient は、拡張機能の ID から、その拡張機能へ最後に問い合わせ（command/execute、provider/invoke など）を始めた client への表である。
	// 拡張機能が後から届ける editor/open、message/show、choice/ask を、その client に出すのに使う（extView。基本設計 1.2）。
	extClient map[string]*clientView
	// extFromPane は、拡張機能の ID のうち、最後の操作がペインの部品の client のキーだったものから、そのキーを処理した時刻への表である
	// （noteViewClient、focusAfterPane）。
	extFromPane map[string]paneMark
	// attachCh は、Attach が渡す新しい client を、イベントループへ送る。
	attachCh chan attachRequest
	// goneCh は、端末の入力が閉じた client を、イベントループへ送る（forwardInput）。
	goneCh chan *clientView
	// keyFireCh は、曖昧なキーの列の時間切れ（client ごとのタイマー）を、イベントループへ送る。
	keyFireCh chan keyFire
	// endErr は、最後の client が終わったときに Run が返す値である（:q などで終われば nil、端末が失われたら ErrTerminalLost）。
	endErr error
	// finished は、Run が戻ると閉じる。Attach が、イベントループの終わりを知るために使う。
	finished chan struct{}
	// diff は、見比べ（diff.go。基本設計 6.8）の状態である。
	diff *diffState

	bufs   []*bufferState
	nextID ext.BufferID

	keys      *keyQueue
	interrupt atomic.Bool
	// forwarded は、端末から読んで列に積んだキーの数である。テストが、送ったキーを処理し終えたかを知るために使う。
	forwarded atomic.Int64
	intrCh    chan struct{}
	// shell は、実行中の外部のコマンド（:{range}! と :r !）である。無ければ nil。shellDone へ結果が届く（shell.go）。
	shell     *shellJob
	shellDone chan shellOutcome
	// termSeq は、端末を子プロセスへ渡す実行の ID の連番である（handover.go）。
	termSeq uint64
	// termCh は、client の端末から届いた、端末を渡した実行の結果を、イベントループへ送る（forwardInput）。
	termCh chan termResult
	// waiters は、待つだけの client（FR-83）である。waitCh と waitGoneCh で足し、外す（handover.go）。
	waiters    []*waiter
	waitCh     chan waitRequest
	waitGoneCh chan *waiter
	// extSync は、実行中の :ext sync である。無ければ nil。進み具合と結果は inspect で届く（extsync.go）。
	extSync *extSyncJob
	// extStopped は、:ext sync が削除の後に Disable で止めた拡張機能の ID である。無効になった知らせを異常として出さない（stateChange）。
	extStopped map[string]bool
	resizeCh   chan struct{}
	// inspect は、別の goroutine から関数をイベントループの goroutine で実行させる。テストが状態を読むのと（do）、
	// :ext sync の進み具合と結果を渡すのに使う（extsync.go）。
	inspect chan func()
	// decoDone は、別の goroutine で作り終えた構文の色の表を、イベントループへ渡す（rebuildDecorations）。
	decoDone chan decoBuilt
	// checkDone は、別の goroutine で確かめた外での変更の結果を、イベントループへ渡す（startCheck）。
	checkDone chan []fileResult
	// checking は、外での変更を別の goroutine で確かめている途中かである。
	checking bool
	// checkAsk は、確かめている途中の結果で、W11〜W13 を問ってよいか（:checktime が求めた確認か）である。
	checkAsk bool
	// extRan は、直前のキーの処理で拡張機能のコマンド（command/execute）を実行したかである。
	// 実行した後は、記録した外での変更の問いを出してよい時点である（afterKey）。
	extRan bool
	// fromCmdline は、直前に処理した利用者のキーを、コマンドラインモードで打ったかである（handleKey）。
	fromCmdline bool
	// statFn は、外での変更の確認で使う stat である。テストが、戻らない stat に差し替える。
	statFn func(string) (fileStamp, error)
	// checkStart は、外での変更の確認を別の goroutine で始めた時刻、checkWarned はその確認が長引いていると知らせたかである。
	checkStart  time.Time
	checkWarned bool
	// checkStall は、確認が長引いていると知らせるまでの時間である。0 なら checkStallTimeout を使う。テストが短くする。
	checkStall time.Duration
	// stop は、Run が戻ると閉じる。イベントループへ渡す先の無くなった goroutine を終わらせる。
	stop chan struct{}

	// ctx は、Run に渡された ctx である。入れ子の待ちも、これが終わったらやめる。
	ctx context.Context

	// held は、入れ子の待ちの間に届き、処理せずに取っておいた拡張機能のイベントである。
	held []exthost.Event
	// waiting は、入れ子の待ちの深さである。
	waiting int
	// tagSeq は、問い合わせの Tag の連番である。
	tagSeq uint64
	// deferred は、期限 0 の設定で結果を待たずに戻った問い合わせである（FR-18）。
	deferred *pendingQuery
	// pendingOpen は、command/execute の結果の Open のうち、別のバッファへ移るもので、Feed が戻ってから行うものである。
	pendingOpen []openRequest

	// msgHistory は、メッセージの履歴（本体の誤りと拡張機能の message/show）を古い順に並べたものである（:messages。messages.go）。
	// client の間で共有する（仮）。
	msgHistory []ui.Message

	// clipPushes は、ホストのクリップボードへ最後に送ったか、送るかを判定したときの command.Registers.Pushes の値である（syncClipboard）。
	clipPushes uint64

	choices []*exthost.ChoiceAskRequest
	// choiceDone は、続けて届いた問い（choices が空になるまでの一続き）のうち、答えたか取り下げられた問いの数である。
	// 表示中の問いが何件目か（choiceDone+1）を出すのに使い、choices が空になったら 0 に戻す（#151）。
	choiceDone int
	// choiceNote は、表示中の問いの上に出す知らせ（前の問いへの答え、捨てたキーの数）である。空なら出さない（#151）。
	choiceNote string
	// choiceDropped は、表示中の問いに対して、答えにならずに捨てたキーと貼り付けの数である。答えるたびに 0 に戻す（#151）。
	choiceDropped int
	// confirm は、表示中の Ex の確認の問いと、問いを出したバッファである。
	confirm   *ex.Confirm
	confirmBS *bufferState
	// prompt は、表示中の本体の問い（外で変わったファイルへの :w の確認、W11〜W13）である。
	prompt  *prompt
	outputs map[outputKey][]string
	// statusOwners は、拡張機能が足したステータス行の部品の ID から、足した拡張機能の ID への表である（buildStatusOwners。FR-92）。
	statusOwners map[string]string
	// extParts は、拡張機能が statusline/set で送った部品の中身である（setStatusPart。FR-92）。
	extParts map[string]ui.ExtPart
	// statusExts は、設定の並びに部品を置いたので起動時に起動した拡張機能の ID の集合である（startStatusExtensions）。
	statusExts map[string]bool
	// outputOrder は、出力の欄を、最初に書き足された順に並べたものである（<C-w>P で切り替える順）。
	outputOrder []outputKey
	// outputLast は、最後に書き足された欄である。
	outputLast outputKey
}

// bufferState は、開いているバッファ 1 つの状態である。
type bufferState struct {
	// owner は、このバッファをタブに持つ編集の client である（FR-62。tabs.go）。どのタブでもなければ nil。
	owner *clientView
	// offscreen は、拡張機能が editor/open（focus が false）で、どのタブにもしないまま開いた、画面に出さないバッファであることを表す
	// （FR-109。offscreen.go）。拡張機能の buffer/save で書き出したら閉じる。タブにしたら下ろす（addTab）。
	offscreen bool
	id        ext.BufferID
	buf       *buffer.Buffer
	state     *command.State
	path      string
	fileType  string
	// title は、ファイルでないバッファ（:help のヘルプ、キーの一覧。help.go）の名前である。タブの行、ステータス行、:ls に
	// path の代わりに出す。ファイルのバッファと、名前の無いバッファ（[No Name]）では空である。
	title string
	// creator は、このバッファを buffer/create で作った拡張機能の ID である（diffstart.go）。それ以外のバッファでは空。
	creator string
	// sent は、exthost へ知らせたバージョン番号である（FR-16）。
	sent uint64
	// replacedInReplay は、マクロの再生の途中の :e で、この bufferState の buf と state を作り直した（replaceBuffer）ことを表す。
	// 作り直す前の State が返す結果は捨てたバッファのものなので、handleResult はその変更を exthost へ送らず、
	// 再生の残りを作り直した State で続ける（PR #173 のレビュー）。handleResult が読んで下ろす。
	replacedInReplay bool
	// popup は、ポップアップのために作ったバッファ（popup.go の newPopupBuffer）であることを表す。どのタブにもならず、:ls に出さない。
	popup bool
	// history は、直近の変更である（基本設計 6.7 の表示のデータ）。
	history     []buffer.Change
	decorations map[string]decorationSet
	decoView    *ui.Decorations
	diagnostics map[string][]ext.Diagnostic
	signs       map[string][]ext.Sign

	// decoBuilding は、decoView を別の goroutine で作っている途中かである（rebuildDecorations）。
	decoBuilding bool
	// decoGen は、拡張機能ごとの一覧の世代の番号である。一覧を消すたびに進める（removeDecorations）。
	// バッファを読み込み直したときは進めない。読み込み直す前の表は、decoBuilt.buf で見分ける。
	decoGen map[string]uint64

	// disk は、バッファとファイルの内容が最後に一致した時点（読み込み、書き出し、読み直し）のファイルの状態である。
	// :w の前に、外で変わっていないかをこれと比べる（Vim の b_mtime_read に当たる）。
	disk fileStamp
	// seen は、外での変更を最後に確かめた時点のファイルの状態である。同じ変更を何度も知らせないために使う（Vim の b_mtime に当たる）。
	seen fileStamp
	// notice は、定期的な確認で見つけ、まだ問うていない外での変更の問い（W11〜W13 の文）である。空なら無い。
	// 問いは打鍵の途中に割り込まないよう、安全な時点（askNotice）まで出さない。読み込み、書き出し、読み直しで消える（setDisk）。
	notice string
}

// New は、エディタを作る。拡張機能は起動しない（FR-14、NFR-04）。バッファは Open で開き、開かなければ Run が空のバッファを作る。
// 設定の誤り（キーマップの表記の誤り、拡張機能のキーの衝突）は、その項目を捨てて作り、最初の 1 件をメッセージで知らせる（基本設計 8 章）。
func New(term Terminal, cfg Config) (*Editor, error) {
	lg := cfg.Logger
	if lg == nil {
		lg = slog.Default()
	}
	if cfg.Host.Logger == nil {
		cfg.Host.Logger = lg
	}
	if cfg.Host.OnPanic == nil {
		cfg.Host.OnPanic = cfg.OnPanic
	}
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("作業ディレクトリを得られない: %w", err)
		}
		cfg.WorkDir = wd
	}
	var sock *exthost.MCPSocket
	if cfg.MCPSocket == "" {
		// 決められなくてもエディタは動かす。本体は待ち受けず、MCP の拡張機能はソケットを引き継がなかったことを mcp.start で利用者へ知らせ、
		// ACP の拡張機能はエージェントへ MCP サーバーを渡さない。
		s, err := exthost.NewMCPSocket()
		if err != nil {
			lg.Warn("MCP のソケットのパスを決められない", "err", err)
		} else {
			sock = s
			cfg.MCPSocket = s.Path
		}
	}
	// 決められなかったときも空の値で上書きし、本体の環境の古い値（ACP のエージェントの中で起動したときの外側のエディタのパスなど）を
	// 拡張機能へ引き継がない。拡張機能と中継コマンドは、空の値を変数が無いのと同じに扱う（os.Getenv）。
	cfg.Host.Env = append(slices.Clone(cfg.Host.Env), ext.EnvMCPSocket+"="+cfg.MCPSocket)
	// 起動の契機 socket の拡張機能（MCP）のために、本体が起動時からこのパスで待ち受ける（基本設計 6.2）。
	if cfg.Host.SocketPath == "" {
		cfg.Host.SocketPath = cfg.MCPSocket
	}
	host, err := exthost.New(cfg.Extensions, cfg.Host)
	if err != nil {
		_ = sock.Remove()
		return nil, err
	}
	e := &Editor{
		cfg:         cfg,
		log:         lg,
		host:        host,
		mcpSocket:   sock,
		keys:        newKeyQueue(),
		intrCh:      make(chan struct{}, 1),
		shellDone:   make(chan shellOutcome, 1),
		termCh:      make(chan termResult),
		waitCh:      make(chan waitRequest),
		waitGoneCh:  make(chan *waiter),
		resizeCh:    make(chan struct{}, 1),
		inspect:     make(chan func()),
		decoDone:    make(chan decoBuilt),
		checkDone:   make(chan []fileResult),
		attachCh:    make(chan attachRequest),
		goneCh:      make(chan *clientView),
		keyFireCh:   make(chan keyFire),
		extClient:   map[string]*clientView{},
		extFromPane: map[string]paneMark{},
		finished:    make(chan struct{}),
		diff:        newDiffState(),
		outputs:     map[outputKey][]string{},
		nextID:      1,
		extParts:    map[string]ui.ExtPart{},
	}
	e.statusOwners = buildStatusOwners(cfg.Extensions)
	e.fileType = defaultFileTypes()
	for k, v := range cfg.FileTypes {
		e.fileType[k] = v
	}
	e.fixEOL = cfg.FixEndOfLine == nil || *cfg.FixEndOfLine
	warnings := slices.Clone(cfg.Problems)
	leader, err := leaderKeys(cfg.Leader)
	if err != nil {
		warnings = append(warnings, err)
	}
	e.leader = leader
	var w1 []error
	e.keymap, w1 = buildKeymap(cfg.Extensions, leader)
	warnings = append(warnings, w1...)
	var w4 []error
	e.popupKeys, w4 = loadPopupKeys(cfg.Extensions, leader)
	warnings = append(warnings, w4...)
	var w3 []error
	e.exCommands, w3 = buildExCommands(cfg.Extensions)
	warnings = append(warnings, w3...)
	if err := addExtManagerCommand(e.exCommands); err != nil {
		warnings = append(warnings, err)
	}
	warnings = append(warnings, addHelpCommands(e.exCommands)...)
	warnings = append(warnings, addMessagesCommand(e.exCommands)...)
	warnings = append(warnings, removedCommandWarnings(cfg.Keymap, e.exCommands)...)
	warnings = append(warnings, e.startStatusExtensions()...)
	var w2 []error
	var remap *remapper
	remap, w2 = newRemapper(cfg.Keymap, leader)
	warnings = append(warnings, w2...)
	warnings = append(warnings, applyKeymapActions(e.keymap, cfg.KeymapActions, leader, remap)...)
	addKeyListBindings(e.keymap, remap)
	// 最初の編集の client は term である（server では、最初に繋いだ client。後の client は Attach で足す）。
	first := e.newClientView(term, remap)
	e.clients = []*clientView{first}
	e.view, e.op = first, first
	e.viewFields = first.v
	for _, w := range warnings {
		lg.Warn("設定の誤り", "err", w)
		e.noteMessage(ui.Message{Text: w.Error(), Level: ui.MessageError})
	}
	if len(warnings) > 0 {
		text := warnings[0].Error()
		if n := len(warnings); n > 1 {
			// 他にも誤りがあることを知らせる。全件は :messages とログ（Config.Logger）にある。
			// 件数と案内は、1 件目の文が長くても端末の幅で切れないように前に置く（#229 のレビューの推奨 5）。
			text = fmt.Sprintf("設定の誤り %d 件（:messages）: %s", n, text)
		}
		// 拡張機能の message/show は、利用者が次のキーを打つまでこれを上書きしない（showExtMessage。#211）。
		e.message = ui.Message{Text: text, Level: ui.MessageError}
	}
	return e, nil
}

// newRenderer は、設定の見た目（色、記号、ステータス行の並び、タブの行）を持った描画の部品を作る。client ごとに 1 つ持つ。
// 色は、本体の既定の対応、選んだ配色（Config.ColorScheme）、設定の colors（Config.Colors）の順に、名前ごとに置き換えて重ねる。
//
// req: FR-125
func (e *Editor) newRenderer() *ui.Renderer {
	return &ui.Renderer{
		Theme:        ui.DefaultTheme().WithColors(e.cfg.ColorScheme).WithColors(e.cfg.Colors),
		Symbols:      e.cfg.Symbols,
		StatusLine:   e.cfg.StatusLine,
		TabLine:      e.cfg.TabLine,
		TabLineParts: e.cfg.TabLineParts,
	}
}

// buildKeymap は、本体の操作に、拡張機能が宣言した操作（contributes）を足したキーマップを作る（FR-11）。
// 登録できない操作（キーの衝突など）は捨て、誤りを返す。
// 宣言の keys の <Leader> は leader に置き換える（FR-97）。
func buildKeymap(specs []exthost.Spec, leader []editcmd.Key) (*input.Keymap, []error) {
	// LSP と DAP の操作の既定のキー（FR-95、FR-96）も leader で登録する。
	km, errs := input.DefaultKeymapLeader(leader)
	for _, s := range specs {
		m := s.Manifest
		groups := []struct {
			kind input.BindingKind
			cs   []ext.Contribution
		}{
			{input.BindOperator, m.Contributes.Operators},
			{input.BindMotion, m.Contributes.Motions},
			{input.BindTextObject, m.Contributes.TextObjects},
			{input.BindAction, m.Contributes.Commands},
		}
		for _, g := range groups {
			for _, c := range g.cs {
				if c.Keys == "" {
					continue
				}
				keys, err := editcmd.ParseKeysLeader(c.Keys, leader)
				if err == nil {
					err = km.Bind(input.Binding{Kind: g.kind, Keys: keys, Ref: editcmd.Ref{ID: c.ID, Extension: m.ID}, Description: c.Description})
				}
				if err != nil {
					errs = append(errs, fmt.Errorf("拡張機能 %s の %s（%s）を登録できない: %w", m.ID, c.ID, c.Keys, err))
				}
			}
		}
	}
	return km, errs
}

// buildExCommands は、拡張機能が宣言したコマンド（contributes.commands）を、コマンドラインから ID で呼ぶための表にする
// （:acp.prompt 説明して。ex.State.ExtCommand）。
// 2 つの拡張機能が同じ ID のコマンドを宣言していたら、先の拡張機能のものを残し、誤りを返す。
// 本体の Ex コマンドと同じ名前の ID は表に残すが、コマンドラインでは本体のコマンドを優先する（ex パッケージの runExtCommand）。
func buildExCommands(specs []exthost.Spec) (map[string]string, []error) {
	cmds := map[string]string{}
	var errs []error
	for _, s := range specs {
		for _, c := range s.Manifest.Contributes.Commands {
			if prev, ok := cmds[c.ID]; ok {
				errs = append(errs, fmt.Errorf("拡張機能 %s のコマンド %s は拡張機能 %s のものと重なるので、コマンドラインからは %s のものを呼ぶ", s.Manifest.ID, c.ID, prev, prev))
				continue
			}
			cmds[c.ID] = s.Manifest.ID
		}
	}
	ms := make([]ext.Manifest, len(specs))
	for i, s := range specs {
		ms[i] = s.Manifest
	}
	providerExCommands(cmds, ms)
	return cmds, errs
}

// extCommand は、コマンドラインのコマンド名 id が拡張機能のコマンドなら、宣言した拡張機能の ID を返す（ex.State.ExtCommand）。
func (e *Editor) extCommand(id string) (string, bool) {
	extID, ok := e.exCommands[id]
	return extID, ok
}

// Close は、拡張機能のプロセスをすべて正常に止める（exthost.Host.CloseContext。基本設計 5.2）。
// ctx が終わったら、止まっていない拡張機能を待たずに SIGKILL で終わらせ、ctx.Err() を返す。
//
// 終わるときの順序は次のとおりにする。
//
//  1. Run が戻る
//  2. 呼び出し側が端末の後始末（画面の復元）をする。拡張機能の終了は最大で数秒かかるので、
//     その間にエディタが止まって見えないように、先に端末を戻す
//  3. Close を呼ぶ
//  4. 端末の入力を閉じる。Run が戻った後も、入力を読む goroutine は Terminal.Input が閉じるまで残る
//
// 拡張機能を止めた後に、New が決めた MCP のソケットのディレクトリを消す（Config.MCPSocket）。
// 消せなかったときは記録だけして、拡張機能の終了の結果を返す。
func (e *Editor) Close(ctx context.Context) error {
	err := e.host.CloseContext(ctx)
	if rerr := e.mcpSocket.Remove(); rerr != nil {
		e.log.Warn("MCP のソケットのディレクトリを消せない", "err", rerr)
	}
	return err
}

// MCPSocket は、拡張機能へ環境変数 ext.EnvMCPSocket で渡す MCP のソケットのパスを返す（Config.MCPSocket）。
// パスを決められなかったときは空を返す。
func (e *Editor) MCPSocket() string { return e.cfg.MCPSocket }

// Open は、path のファイルを開き、カーソルのあるバッファにする。
// ファイルが無ければ、そのパスの空のバッファを作る（Vim の :e newfile と同じ）。
// 既に開いているファイルなら、そのバッファへ移る。
// 起動の引数のファイルを開くためのもので、ジャンプリストと裏のバッファ（<C-^> の移り先）には記録しない
// （Vim が引数のファイルを開いたときと同じ）。バッファの番号は、開いた順に 1 から付ける。
func (e *Editor) Open(path string) error {
	return e.OpenWith(path, e.cfg.ReadOnlyArgs)
}

// OpenWith は Open と同じく起動の引数のファイルを開く。Config.ReadOnlyArgs に代えて、readOnly で編集を禁止するかを決める。
// 段階 3 の server が、繋いだ編集の client の引数のファイルを、その client の -R に従って開くのに使う（#201）。
func (e *Editor) OpenWith(path string, readOnly bool) error {
	bs, err := e.openFile(path, true)
	e.jumps, e.jumpIdx, e.alt = nil, 0, nil
	if err == nil && readOnly {
		// req: FR-119
		bs.buf.SetReadOnly(true)
	}
	return err
}

// Run は、イベントループを動かす。:q で終了するか、端末の入力が閉じるか、ctx が終わるまで戻らない。
// :q では nil を、端末の入力の終わりでは ErrTerminalLost を、ctx の終わりでは ctx.Err() を返す。
// 拡張機能の応答を待っている途中（入れ子の待ち）に ctx が終わっても、待ちをやめて戻る。
// 戻った後の後始末の順序は Close の doc にある。
//
// 拡張機能の起動は待たない。起動は exthost の goroutine で進み、その間もキーを処理する（NFR-04）。
//
// req: NFR-04
func (e *Editor) Run(ctx context.Context) error {
	defer close(e.finished)
	e.ctx = ctx
	e.stop = make(chan struct{})
	defer close(e.stop)
	for _, c := range e.clients {
		e.startClient(c)
	}
	var checkC <-chan time.Time
	if d := e.checkInterval(); d > 0 {
		t := time.NewTicker(d)
		defer t.Stop()
		checkC = t.C
	}
	e.render()
	for {
		// 終わる client（:q で最後のタブを閉じた、端末の入力が閉じた）を外す。最後の client が終われば戻る（FR-61、FR-114）。
		if e.reapClients(); len(e.clients) == 0 {
			return e.endErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(e.held) > 0 && !e.asking() {
			ev := e.held[0]
			e.held = e.held[1:]
			e.handleEvent(ev)
			e.renderIdle()
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case c := <-e.goneCh:
			c.inputClosed = true
		case req := <-e.attachCh:
			e.attach(req)
		case f := <-e.keyFireCh:
			// 外部のコマンドか再生中の拡張機能の結果を待っている client は、待ちが終わってから次のキーで掛け直す（keyAllowed）。
			if f.c.attached() && f.gen == f.c.keyGen && e.keyAllowed(f.c) {
				e.useClient(f.c)
				e.flushKeys()
			}
		case <-e.keys.ready:
			e.handleKeys()
		case <-e.resizeCh:
			e.applySize()
		case <-e.intrCh:
			// <C-c> のキーそのものは列から届くので、ここでは外部のコマンドと再生中の拡張機能の待ちの中断だけをする。
			e.interruptShell()
			e.interruptReplay()
		case o := <-e.shellDone:
			e.shellFinished(o)
		case r := <-e.termCh:
			e.terminalResult(r)
		case req := <-e.waitCh:
			e.addWaiter(req)
		case w := <-e.waitGoneCh:
			e.waiterGone(w)
		case ev, ok := <-e.host.Events():
			if !ok {
				return nil
			}
			if e.asking() {
				// 確認の問いに答えるまでは、Vim の問いと同じく他の処理をしない。問いを出したバッファと範囲を変えないためである。
				e.hold(ev)
			} else {
				e.handleEvent(ev)
			}
		case f := <-e.inspect:
			f()
		case r := <-e.decoDone:
			e.decorationsBuilt(r)
		case r := <-e.diff.done:
			e.diffBuilt(r)
		case <-checkC:
			if e.checkStalled() {
				break // 確認が終わらないことを知らせたので描き直す
			}
			if e.idle() {
				e.startCheck(false)
			}
			continue // 確かめ始めただけで、画面は変わらない
		case rs := <-e.checkDone:
			if !e.applyCheck(rs) {
				continue // 何も変わらなければ描き直さない
			}
		}
		if e.reapClients(); len(e.clients) > 0 {
			e.renderIdle()
		}
	}
}

// renderIdle は、列に処理できるキーが無いときだけ描く。
// 貼り付けのようにキーがまとめて届いたときは、溜まったキーを処理し終えてから 1 回描く。
// 外部のコマンドの実行中や問いの間に止めている client のキー（keyAllowed）は数えない。
func (e *Editor) renderIdle() {
	if !e.keys.any(e.keyAllowed) {
		e.render()
	}
}

// recoverPanic は、Editor が起こす goroutine の始めに defer で呼ぶ（他の defer より後に書き、先に動かす）。
// panic していれば、Config.OnPanic を呼んでから同じ値で panic を続ける（基本設計 8 章）。
func (e *Editor) recoverPanic() {
	if e.cfg.OnPanic == nil {
		return
	}
	if p := recover(); p != nil {
		e.cfg.OnPanic()
		panic(p)
	}
}

// signal は、容量 1 のチャネルへ、詰まらずに知らせを送る。
func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// do は、f をイベントループの goroutine で実行し、終わるまで待つ。テストが状態を読むために使う。
// Run が戻った後は、f をこの goroutine で呼ぶ（イベントループはもう状態を触らない）。
func (e *Editor) do(f func()) {
	done := make(chan struct{})
	select {
	case e.inspect <- func() {
		defer close(done)
		f()
	}:
		<-done
	case <-e.finished:
		f()
	}
}

// newBuffer は、バッファを開いた状態に加え、exthost へ知らせる。カーソルのあるバッファがまだ無ければ、そのバッファにする。
// 他のバッファがあるときは移らない（移るのは呼び出し側が enter か switchTo で行う）。
// exthost は、fileType の契機を宣言した未起動の拡張機能をここで初めて起動する（FR-14）。
//
// req: FR-14
func (e *Editor) newBuffer(b *buffer.Buffer, path, fileType string) *bufferState {
	bs := &bufferState{
		id:          e.nextID,
		buf:         b,
		path:        path,
		fileType:    fileType,
		sent:        b.Version(),
		decorations: map[string]decorationSet{},
		diagnostics: map[string][]ext.Diagnostic{},
		signs:       map[string][]ext.Sign{},
	}
	e.nextID++
	e.initState(bs)
	e.bufs = append(e.bufs, bs)
	if e.cur == nil {
		e.addTab(bs)
		e.cur = bs
		if e.shared == nil {
			e.shared = bs.state
		}
	}
	return bs
}

// initState は、bs.buf を編集する command.State を作り、exthost へ buffer/open で知らせる。
func (e *Editor) initState(bs *bufferState) {
	b := bs.buf
	bs.sent = b.Version()
	bs.state = command.New(b, input.NewParser(e.keymap))
	// レジスターはバッファを切り替えるたびに TakeOver で引き継ぐので、ここでの設定は最初のバッファのものが全体に残る。
	bs.state.Registers().SetConfig(e.cfg.Registers)
	bs.state.Columns = e.renderer.Columns()
	bs.state.Viewport = viewport{r: e.renderer}
	bs.state.Extensions = &extQuery{e: e, bs: bs}
	bs.state.Navigator = navigator{e: e, bs: bs, st: bs.state}
	bs.state.Ex.ExtCommand = e.extCommand
	bs.state.Interrupted = e.interrupt.Load
	bs.state.KeyHook = func(k editcmd.Key, insert bool) (bool, []editcmd.Key, error) { return e.popupStateKey(bs, k, insert) }
	if err := e.host.OpenBuffer(exthost.BufferSnapshot{
		ID: bs.id, Path: bs.path, FileType: bs.fileType, Version: b.Version(), Text: b.Text(),
		NoFinalNewline: !finalNewline(b),
	}); err != nil {
		e.log.Error("buffer/open を送れない", "err", err)
	}
}

// finalNewline は、拡張機能へ送る buffer/open の finalNewline（書き出したときに最後の行の後に改行が付くか）を返す。
// 行の無いバッファは空のファイルとして書き出すので false にする（基本設計 4.1）。
func finalNewline(b *buffer.Buffer) bool {
	return b.FinalNewline() && !b.NoLines()
}

// syncFinalNewline は、bs の finalNewline が拡張機能へ送った値と変わっていれば、buffer/open で送り直させる（exthost.Host.SetFinalNewline）。
// 行の無いバッファになったとき（全ての行の削除）と、そうでなくなったとき（入力、アンドゥ、:e）に変わる。
// 変更（buffer/change）を送った後に呼ぶ。1 つの Result に複数の変更があるときは、各変更の後に最後の状態の値で呼ぶので、
// 途中の値は送らないことがあるが、最後に送る値は現在のバッファと一致する。
func (e *Editor) syncFinalNewline(bs *bufferState) {
	if err := e.host.SetFinalNewline(bs.id, finalNewline(bs.buf)); err != nil {
		e.log.Error("finalNewline を送り直せない", "buffer", bs.id, "err", err)
	}
}

// switchTo は、カーソルのあるバッファを bs にする。レジスター、直前の検索、ドットリピート、マクロの記録などの
// Vim でエディタ全体に 1 つの状態は引き継ぐ（command.State.TakeOver）。
// 別のバッファへ移るときは、画面の位置を、bs に残したカーソルを中ほどに置く位置に決め直す（ui.Renderer.EnterBuffer。Vim 9.1 の enter_buffer）。
// 移った後に呼び出し側がカーソルを動かせば（<C-o> の移り先、定義へ移動）、次の描画がその位置から画面に収める。
func (e *Editor) switchTo(bs *bufferState) {
	if e.cur != nil && e.cur != bs {
		bs.state.TakeOver(e.cur.state)
		e.renderer.EnterBuffer(bs.buf.LineCount(), bs.state.Cursor().Line)
		if e.shared == e.cur.state {
			e.shared = bs.state
		}
	}
	e.cur = bs
}

// findBuffer は、番号が id のバッファを返す。
func (e *Editor) findBuffer(id ext.BufferID) *bufferState {
	for _, bs := range e.bufs {
		if bs.id == id {
			return bs
		}
	}
	return nil
}

// absPath は、path を作業ディレクトリをもとに絶対パスにする。シンボリックリンクは解決しない（基本設計 6.7 のバッファのパス）。
func (e *Editor) absPath(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(e.workDir(), path)
}

// openFile は、path のファイルを開く。既に開いていればそのバッファを返す。
// focus が true ならカーソルのあるバッファにし、移る前の位置をジャンプリストに足す（enter）。
// focus が true で、カーソルのあるバッファが起動したときの空の名前の無いバッファ（reusable）なら、
// 新しい番号を作らずにそのバッファを開いたファイルのものにする（Vim と同じく、:ls に [No Name] を残さない）。
func (e *Editor) openFile(path string, focus bool) (*bufferState, error) {
	abs := e.absPath(path)
	for _, bs := range e.bufs {
		if bs.path == abs {
			if focus {
				e.enter(bs)
			}
			return bs, nil
		}
	}
	// 状態は読む前に得る。読む間に書き換えられても、次の確認で変更として見つかる（読んだ後に得ると、その変更を見逃す）。
	st, _ := statFile(abs)
	b, err := buffer.ReadFile(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// 新しいファイルは、Vim と同じく行の無いバッファにする。編集せずに書き出すと空のファイルに、編集すれば末尾に改行を付けて書き出す（基本設計 4.1）。
		b = buffer.New("")
	case err != nil:
		return nil, fmt.Errorf("%s を開けない: %w", path, err)
	}
	// :e での読み直し（reload）は、元のバッファの設定を引き継ぐので、ここで設定するのは開くときだけである。
	b.SetFixEndOfLine(e.fixEOL)
	// req: FR-120
	b.SetReadOnly(underReadOnlyPath(abs, e.cfg.ReadOnlyPaths))
	prev := e.cur
	if focus && reusable(prev) {
		// Vim 9.1 と同じく、移る前の位置（1 行目）をジャンプリストに足す。番号が同じなので、開いたファイルの 1 行目を指す。
		e.markJumpAt(prev, prev.state.Cursor())
		prev.path, prev.fileType = abs, e.detectFileType(abs)
		e.replaceBuffer(prev, b)
		prev.setDisk(st)
		return prev, nil
	}
	bs := e.newBuffer(b, abs, e.detectFileType(abs))
	bs.setDisk(st)
	if focus {
		e.enter(bs)
	}
	return bs, nil
}

// reusable は、bs が、別のファイルを開くときにそのファイルのバッファとして使い直せる、名前の無い空のバッファかを返す。
// 名前が無く、変更が無く、テキストが空のバッファである（Vim が起動したときのバッファを :e で使い直すのと同じ）。
func reusable(bs *bufferState) bool {
	return bs != nil && bs.path == "" && bs.title == "" && !bs.buf.Modified() && bs.buf.LineCount() == 1 && bs.buf.Line(0) == ""
}

// replaceBuffer は、バッファ bs の内容を b に置き換える。番号は変えず、拡張機能へは同じ番号で buffer/close と buffer/open を送り直す。
// バージョン番号は b のもの（読み込んだばかりなら 0）から数え直し、アンドゥの履歴と、診断と印と色は消す。
// エディタ全体に 1 つの状態（command.State.TakeOver）とカーソルの位置は引き継ぐ。bs.path と bs.fileType は、呼ぶ前に新しい値にしておく。
func (e *Editor) replaceBuffer(bs *bufferState, b *buffer.Buffer) {
	if err := e.host.CloseBuffer(bs.id); err != nil {
		e.log.Error("buffer/close を送れない", "err", err)
	}
	old := bs.state
	bs.buf = b
	bs.history = nil
	bs.decorations = map[string]decorationSet{}
	bs.decoView = nil
	bs.diagnostics = map[string][]ext.Diagnostic{}
	bs.signs = map[string][]ext.Sign{}
	e.initState(bs)
	bs.state.TakeOver(old)
	bs.state.SetCursor(old.Cursor())
	if e.shared == old {
		e.shared = bs.state
	}
	if bs.owner != nil {
		bs.state.Viewport = viewport{r: e.viewOf(bs.owner).renderer}
	}
}

// detectFileType は、ファイル名と拡張子から、ファイルの種類を判定する（基本設計 6.7 のファイルの種類の判定）。
func (e *Editor) detectFileType(path string) string {
	base := filepath.Base(path)
	if ft, ok := e.fileType[base]; ok {
		return ft
	}
	return e.fileType[filepath.Ext(base)]
}

// defaultFileTypes は、ファイルの種類の既定の対応表（仮）を返す。
func defaultFileTypes() map[string]string {
	return map[string]string{
		".go": "go", "go.mod": "gomod", ".md": "markdown", ".json": "json", ".jsonc": "jsonc",
		".py": "python", ".c": "c", ".h": "c", ".js": "javascript", ".ts": "typescript", ".rs": "rust",
		".sh": "sh", ".yaml": "yaml", ".yml": "yaml", ".toml": "toml", "Makefile": "make",
	}
}

// syncTimeout は、キー入力ごとの問い合わせの期限を返す。
func (e *Editor) syncTimeout() time.Duration {
	if e.cfg.SyncTimeout == nil {
		return DefaultSyncTimeout
	}
	return max(*e.cfg.SyncTimeout, 0)
}

// startSyncTimeout は、起動を含む問い合わせの期限を返す。
func (e *Editor) startSyncTimeout() time.Duration {
	if e.cfg.StartSyncTimeout <= 0 {
		return DefaultStartSyncTimeout
	}
	return e.cfg.StartSyncTimeout
}

// keyTimeout は、曖昧なキーの列を確定させるまでの待ち時間を返す。
func (e *Editor) keyTimeout() time.Duration {
	if e.cfg.KeyTimeout <= 0 {
		return DefaultKeyTimeout
	}
	return e.cfg.KeyTimeout
}

// showError は、誤りを 1 行のメッセージで知らせ、メッセージの履歴に残す（基本設計 8 章。:messages）。
// 次のキーで消えるまで、拡張機能の message/show はこれを上書きしない（showExtMessage）。
func (e *Editor) showError(err error) {
	e.message = ui.Message{Text: err.Error(), Level: ui.MessageError}
	e.noteMessage(e.message)
}

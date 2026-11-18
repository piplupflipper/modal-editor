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

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/command/ex"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/input"
	"github.com/piplupflipper/modal-editor/internal/ui"
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
	// 外部のコマンドはイベントループを止めずに動くので、子が起動した modal-editor（FR-83）が server に繋いでも詰まらない。
	// 同じ名前は後のものが勝つ（os/exec の決まり）。
	// FR-82 の server のソケットのパス（仮に MODAL_EDITOR_SOCKET）と、EDITOR、GIT_EDITOR を入れる口で、
	// 値は server のソケットができてから呼び出し側が入れる（#190）。
	ChildEnv []string
}

// Editor は、イベントループと、編集の状態の全体である。New で作り、Run で動かし、Close で終える。
// Run 以外のメソッド（Open）は、Run の前に、Run と同じ goroutine から呼ぶ。
type Editor struct {
	cfg      Config
	log      *slog.Logger
	term     Terminal
	host     *exthost.Host
	renderer *ui.Renderer
	keymap   *input.Keymap
	// exCommands は、コマンドラインから呼べる拡張機能のコマンドの ID から、宣言した拡張機能の ID への表である（buildExCommands）。
	exCommands map[string]string
	remap      *remapper
	// leader は、<Leader> を置き換えたキーの列（Config.Leader を読んだもの。FR-97）である。キーの一覧（FR-90）で使う。
	leader   []editcmd.Key
	fileType map[string]string
	// fixEOL は、開くバッファに設定する末尾の改行の補い（Config.FixEndOfLine）である。
	fixEOL bool

	// mcpSocket は、New が決めた MCP のソケットのパスである。Config.MCPSocket を指定したときと、決められなかったときは nil。
	mcpSocket *exthost.MCPSocket

	bufs   []*bufferState
	cur    *bufferState
	nextID ext.BufferID
	// alt は、裏のバッファ（直前にカーソルのあったバッファ。<C-^> の移り先、:ls の #）である。無ければ nil。
	alt *bufferState
	// jumps と jumpIdx は、ジャンプリストと、<C-o> と <C-i> が次に読む位置である（buffers.go）。
	// jumpIdx が len(jumps) なら、末尾の次（最後のジャンプの後に <C-o> をまだ打っていない）を指す。
	jumps   []jumpEntry
	jumpIdx int

	keys      *keyQueue
	interrupt atomic.Bool
	// forwarded は、端末から読んで列に積んだキーの数である。テストが、送ったキーを処理し終えたかを知るために使う。
	forwarded atomic.Int64
	intrCh    chan struct{}
	// shell は、実行中の外部のコマンド（:{range}! と :r !）である。無ければ nil。shellDone へ結果が届く（shell.go）。
	shell     *shellJob
	shellDone chan shellOutcome
	resizeCh  chan struct{}
	size      atomic.Value // [2]int
	inputDone chan struct{}
	inspect   chan func()
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

	keyTimer *time.Timer
	keyTimeC <-chan time.Time

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

	// clipPushes は、ホストのクリップボードへ最後に送ったか、送るかを判定したときの command.Registers.Pushes の値である（syncClipboard）。
	clipPushes uint64

	message ui.Message
	popup   *popupState
	// regList は、:registers で出したレジスターの一覧の窓である（reglist.go）。nil なら出していない。
	regList *regListState
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
	// outputOrder は、出力の欄を、最初に書き足された順に並べたものである（<C-w>P で切り替える順）。
	outputOrder []outputKey
	// outputLast は、最後に書き足された欄である。
	outputLast outputKey
	// outView は、開いている出力の欄である。nil なら閉じている。
	outView *outputView

	quit bool
}

// bufferState は、開いているバッファ 1 つの状態である。
type bufferState struct {
	id       ext.BufferID
	buf      *buffer.Buffer
	state    *command.State
	path     string
	fileType string
	// sent は、exthost へ知らせたバージョン番号である（FR-16）。
	sent uint64
	// replacedInReplay は、マクロの再生の途中の :e で、この bufferState の buf と state を作り直した（replaceBuffer）ことを表す。
	// 作り直す前の State が返す結果は捨てたバッファのものなので、handleResult はその変更を exthost へ送らず、
	// 再生の残りを作り直した State で続ける（PR #173 のレビュー）。handleResult が読んで下ろす。
	replacedInReplay bool
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
		cfg:       cfg,
		log:       lg,
		term:      term,
		host:      host,
		mcpSocket: sock,
		renderer:  &ui.Renderer{},
		keys:      newKeyQueue(),
		intrCh:    make(chan struct{}, 1),
		shellDone: make(chan shellOutcome, 1),
		resizeCh:  make(chan struct{}, 1),
		inputDone: make(chan struct{}),
		inspect:   make(chan func()),
		decoDone:  make(chan decoBuilt),
		checkDone: make(chan []fileResult),
		outputs:   map[outputKey][]string{},
		nextID:    1,
	}
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
	var w3 []error
	e.exCommands, w3 = buildExCommands(cfg.Extensions)
	warnings = append(warnings, w3...)
	var w2 []error
	e.remap, w2 = newRemapper(cfg.Keymap, leader)
	warnings = append(warnings, w2...)
	for _, w := range warnings {
		lg.Warn("設定の誤り", "err", w)
	}
	if len(warnings) > 0 {
		text := warnings[0].Error()
		if n := len(warnings) - 1; n > 0 {
			// 他にも誤りがあることを知らせる。全件はログ（Config.Logger）にある。
			text = fmt.Sprintf("%s（他 %d 件）", text, n)
		}
		e.message = ui.Message{Text: text, Level: ui.MessageError}
	}
	return e, nil
}

// buildKeymap は、本体の操作に、拡張機能が宣言した操作（contributes）を足したキーマップを作る（FR-11）。
// 登録できない操作（キーの衝突など）は捨て、誤りを返す。
// 宣言の keys の <Leader> は leader に置き換える（FR-97）。
func buildKeymap(specs []exthost.Spec, leader []editcmd.Key) (*input.Keymap, []error) {
	km := input.DefaultKeymap()
	var errs []error
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
					err = km.Bind(input.Binding{Kind: g.kind, Keys: keys, Ref: editcmd.Ref{ID: c.ID, Extension: m.ID}})
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
	bs, err := e.openFile(path, true)
	e.jumps, e.jumpIdx, e.alt = nil, 0, nil
	if err == nil && e.cfg.ReadOnlyArgs {
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
	if e.cur == nil {
		b := buffer.New("")
		b.SetFixEndOfLine(e.fixEOL)
		e.newBuffer(b, "", "")
	}
	w, h := e.term.Size()
	e.setSize(w, h)
	e.ctx = ctx
	e.stop = make(chan struct{})
	defer close(e.stop)
	go e.forwardInput()
	var checkC <-chan time.Time
	if d := e.checkInterval(); d > 0 {
		t := time.NewTicker(d)
		defer t.Stop()
		checkC = t.C
	}
	e.render()
	for !e.quit {
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
		case <-e.inputDone:
			if e.keys.empty() || e.shell != nil {
				return ErrTerminalLost
			}
			e.handleKeys()
		case <-e.keys.ready:
			e.handleKeys()
		case <-e.resizeCh:
			e.applySize()
		case <-e.intrCh:
			// <C-c> のキーそのものは列から届くので、ここでは外部のコマンドの中断だけをする。
			e.interruptShell()
		case o := <-e.shellDone:
			e.shellFinished(o)
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
		case <-e.keyTimeC:
			e.keyTimeC = nil
			e.flushKeys()
		case f := <-e.inspect:
			f()
		case r := <-e.decoDone:
			e.decorationsBuilt(r)
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
		e.renderIdle()
	}
	return nil
}

// renderIdle は、列に処理していないキーが無いときだけ描く。
// 貼り付けのようにキーがまとめて届いたときは、溜まったキーを処理し終えてから 1 回描く。
func (e *Editor) renderIdle() {
	if e.keys.empty() {
		e.render()
	}
}

// forwardInput は、端末の入力を読み、キーを列に積む。<C-c> を見たら中断の印を立てる。
func (e *Editor) forwardInput() {
	defer close(e.inputDone)
	defer e.recoverPanic()
	for ev := range e.term.Input() {
		if ev.Resize {
			e.size.Store([2]int{ev.Width, ev.Height})
			signal(e.resizeCh)
			continue
		}
		if ev.Paste != "" {
			e.keys.push(keyInput{paste: ev.Paste})
			e.forwarded.Add(1)
			continue
		}
		k := ev.Key.Normalize()
		if k == interruptKey {
			e.interrupt.Store(true)
			signal(e.intrCh)
		}
		e.keys.push(keyInput{key: k})
		e.forwarded.Add(1)
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

func (e *Editor) setSize(w, h int) {
	e.renderer.Width, e.renderer.Height = max(w, 0), max(h, 0)
}

// applySize は、端末から届いた最新の大きさを反映する。
func (e *Editor) applySize() {
	if v, ok := e.size.Load().([2]int); ok {
		e.setSize(v[0], v[1])
	}
}

// do は、f をイベントループの goroutine で実行し、終わるまで待つ。テストが状態を読むために使う。
func (e *Editor) do(f func()) {
	done := make(chan struct{})
	e.inspect <- func() {
		defer close(done)
		f()
	}
	<-done
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
		e.cur = bs
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
	return filepath.Join(e.cfg.WorkDir, path)
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
	return bs != nil && bs.path == "" && !bs.buf.Modified() && bs.buf.LineCount() == 1 && bs.buf.Line(0) == ""
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

// showError は、誤りを 1 行のメッセージで知らせる（基本設計 8 章）。
func (e *Editor) showError(err error) {
	e.message = ui.Message{Text: err.Error(), Level: ui.MessageError}
}

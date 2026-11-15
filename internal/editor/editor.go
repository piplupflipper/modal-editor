package editor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
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
	// 期限の既定値は #43 の裁定待ちで、段階 1 の計測からの提案値（PR #37）を仮に使う。
	DefaultSyncTimeout = 100 * time.Millisecond
	// DefaultKeyTimeout は、曖昧なキーの列（利用者のキーマップの接頭辞、本体の y と拡張機能の ys）を確定させるまでの待ち時間である。
	// Vim の 'timeoutlen' の既定と同じ 1000 ms にした。
	DefaultKeyTimeout = time.Second
	// maxHistory は、診断と印をずらすために、バッファごとに覚えておく直近の変更の数である（基本設計 6.7。仮に 100）。
	maxHistory = 100
	// maxOutputLines は、出力の欄 1 つあたりに持つ行数の上限である（基本設計 6.7。仮に 10000）。
	maxOutputLines = 10000
)

// InterruptKey は、中断のキー（<C-c>）である。
// マクロの再生と拡張機能の応答の待ちを止める。それ以外のときは <Esc> として扱う。
var InterruptKey = editcmd.Key{Code: editcmd.CodeRune, Rune: 'c', Mod: editcmd.ModCtrl}

// Terminal は、端末の口である。端末の制御のライブラリ（#9 で裁定待ち）を包んだものを、呼び出し側が差し込む。
type Terminal interface {
	// Input は、キーとリサイズのイベントを届いた順に送るチャネルを返す。
	// 閉じると、Run は終わる（端末が失われた）。
	Input() <-chan TermEvent
	// Size は、端末の現在の列数と行数を返す。Run の始めに 1 回だけ呼ぶ。
	Size() (width, height int)
	// Draw は、画面の内容とカーソルを端末へ出す。前回との差分だけを書くのは実装の自由である。
	// Run の goroutine から呼ぶ。誤りはログに記録し、編集は続ける。
	Draw(s ui.Screen) error
}

// TermEvent は、端末からのイベント 1 件である。
type TermEvent struct {
	// Key は、押されたキーである。Resize が true なら読まない。
	Key editcmd.Key
	// Resize は、端末の大きさが Width と Height に変わったことを表す。
	Resize        bool
	Width, Height int
}

// Config は、エディタの設定である。ゼロ値の項目は既定値を使う。
// 設定ファイル（FR-40、FR-41）の読み込みは #9 の裁定の後に作り、読み込んだ値をこの形で渡す。
type Config struct {
	// Keymap は、利用者のキーマップ（基本設計 7 章の keymap）である。
	// モードの名前（"normal"、"visual"、"insert"、"commandLine"）から、キーの列の表記から展開するキーの列の表記への対応である。
	// 展開した列は、もう一度キーマップで展開しない（Vim の noremap と同じ）。
	// 拡張機能の起動を待たずに反映する（FR-41）。
	Keymap map[string]map[string]string
	// SyncTimeout は、キー入力ごとに結果が要る問い合わせの期限（基本設計 7 章の syncTimeoutMs）である。
	// nil なら DefaultSyncTimeout を使う。0 は「待たない」（FR-18）。
	SyncTimeout *time.Duration
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
	// Logger は、ログの記録先である。nil なら slog.Default() を使う。
	Logger *slog.Logger
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
	remap    *remapper
	fileType map[string]string

	bufs   []*bufferState
	cur    *bufferState
	nextID ext.BufferID

	keys      *keyQueue
	interrupt atomic.Bool
	intrCh    chan struct{}
	resizeCh  chan struct{}
	size      atomic.Value // [2]int
	inputDone chan struct{}
	inspect   chan func()

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

	message ui.Message
	popup   *popupState
	choices []*exthost.ChoiceAskRequest
	confirm *ex.Confirm
	outputs map[outputKey][]string

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
	// history は、直近の変更である（基本設計 6.7 の表示のデータ）。
	history     []buffer.Change
	decorations map[string]decorationSet
	decoView    *ui.Decorations
	diagnostics map[string][]ext.Diagnostic
	signs       map[string][]ext.Sign
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
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("作業ディレクトリを得られない: %w", err)
		}
		cfg.WorkDir = wd
	}
	host, err := exthost.New(cfg.Extensions, cfg.Host)
	if err != nil {
		return nil, err
	}
	e := &Editor{
		cfg:       cfg,
		log:       lg,
		term:      term,
		host:      host,
		renderer:  &ui.Renderer{},
		keys:      newKeyQueue(),
		intrCh:    make(chan struct{}, 1),
		resizeCh:  make(chan struct{}, 1),
		inputDone: make(chan struct{}),
		inspect:   make(chan func()),
		outputs:   map[outputKey][]string{},
		nextID:    1,
	}
	e.fileType = defaultFileTypes()
	for k, v := range cfg.FileTypes {
		e.fileType[k] = v
	}
	var warnings []error
	e.keymap, warnings = buildKeymap(cfg.Extensions)
	var w2 []error
	e.remap, w2 = newRemapper(cfg.Keymap)
	warnings = append(warnings, w2...)
	for _, w := range warnings {
		lg.Warn("設定の誤り", "err", w)
	}
	if len(warnings) > 0 {
		e.message = ui.Message{Text: warnings[0].Error(), Level: ui.MessageError}
	}
	return e, nil
}

// buildKeymap は、本体の操作に、拡張機能が宣言した操作（contributes）を足したキーマップを作る（FR-11）。
// 登録できない操作（キーの衝突など）は捨て、誤りを返す。
func buildKeymap(specs []exthost.Spec) (*input.Keymap, []error) {
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
				keys, err := editcmd.ParseKeys(c.Keys)
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

// Close は、拡張機能のプロセスをすべて終わらせる。Run が戻った後に呼ぶ。
func (e *Editor) Close() error { return e.host.Close() }

// Host は、拡張機能を管理する exthost.Host を返す。
func (e *Editor) Host() *exthost.Host { return e.host }

// Open は、path のファイルを開き、カーソルのあるバッファにする。
// ファイルが無ければ、そのパスの空のバッファを作る（Vim の :e newfile と同じ）。
// 既に開いているファイルなら、そのバッファへ移る。
func (e *Editor) Open(path string) error {
	_, err := e.openFile(path, true)
	return err
}

// Run は、イベントループを動かす。:q で終了するか、端末の入力が閉じるか、ctx が終わるまで戻らない。
// :q と端末の入力の終わりでは nil を、ctx の終わりでは ctx.Err() を返す。
func (e *Editor) Run(ctx context.Context) error {
	if e.cur == nil {
		e.newBuffer(buffer.New(""), "", "")
	}
	w, h := e.term.Size()
	e.setSize(w, h)
	go e.forwardInput()
	e.render()
	for !e.quit {
		if len(e.held) > 0 {
			ev := e.held[0]
			e.held = e.held[1:]
			e.handleEvent(ev)
			e.render()
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.inputDone:
			if e.keys.empty() {
				return nil
			}
			e.handleKeys()
		case <-e.keys.ready:
			e.handleKeys()
		case <-e.resizeCh:
			e.applySize()
		case <-e.intrCh:
			// <C-c> のキーそのものは列から届くので、ここでは何もしない。
		case ev, ok := <-e.host.Events():
			if !ok {
				return nil
			}
			e.handleEvent(ev)
		case <-e.keyTimeC:
			e.keyTimeC = nil
			e.flushKeys()
		case f := <-e.inspect:
			f()
		}
		e.render()
	}
	return nil
}

// forwardInput は、端末の入力を読み、キーを列に積む。<C-c> を見たら中断の印を立てる。
func (e *Editor) forwardInput() {
	defer close(e.inputDone)
	for ev := range e.term.Input() {
		if ev.Resize {
			e.size.Store([2]int{ev.Width, ev.Height})
			signal(e.resizeCh)
			continue
		}
		k := ev.Key.Normalize()
		if k == InterruptKey {
			e.interrupt.Store(true)
			signal(e.intrCh)
		}
		e.keys.push(k)
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

// newBuffer は、バッファを開いた状態に加え、exthost へ知らせ、カーソルのあるバッファにする。
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
	bs.state = command.New(b, input.NewParser(e.keymap))
	bs.state.Columns = e.renderer.Columns()
	bs.state.Extensions = &extQuery{e: e, bs: bs}
	bs.state.Interrupted = e.interrupt.Load
	if err := e.host.OpenBuffer(exthost.BufferSnapshot{ID: bs.id, Path: path, FileType: fileType, Version: b.Version(), Text: b.Text()}); err != nil {
		e.log.Error("buffer/open を送れない", "err", err)
	}
	e.bufs = append(e.bufs, bs)
	e.switchTo(bs)
	return bs
}

// switchTo は、カーソルのあるバッファを bs にする。レジスターは引き継ぐ（Vim と同じく、レジスターはバッファをまたいで 1 組）。
func (e *Editor) switchTo(bs *bufferState) {
	if e.cur != nil && e.cur != bs {
		*bs.state.Registers() = *e.cur.state.Registers()
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

// openFile は、path のファイルを開く。既に開いていればそのバッファを返す。focus が true ならカーソルのあるバッファにする。
func (e *Editor) openFile(path string, focus bool) (*bufferState, error) {
	abs := e.absPath(path)
	for _, bs := range e.bufs {
		if bs.path == abs {
			if focus {
				e.switchTo(bs)
			}
			return bs, nil
		}
	}
	b, err := buffer.ReadFile(abs)
	switch {
	case errors.Is(err, os.ErrNotExist):
		b = buffer.New("")
	case err != nil:
		return nil, fmt.Errorf("%s を開けない: %w", path, err)
	}
	prev := e.cur
	bs := e.newBuffer(b, abs, e.detectFileType(abs))
	if !focus && prev != nil {
		e.cur = prev
	}
	return bs, nil
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

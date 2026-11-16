// Command modal-editor は、エディタ本体の実行ファイルである。
//
// 引数のファイルを開き、端末（標準入力と標準出力）でエディタを動かす。
// 端末の制御は internal/term、編集のイベントループは internal/editor が担い、このコマンドは両者をつなぐ。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/piplupflipper/modal-editor/internal/config"
	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/term"
	"github.com/piplupflipper/modal-editor/internal/version"
)

// closeTimeout は、終了のときに拡張機能が止まるのを待つ上限である。超えたら残りを SIGKILL で終わらせる（editor.Editor.Close）。
const closeTimeout = 5 * time.Second

// wrapLogHandler は、テストだけが設定する。nil でなければ、edit はロガーの Handler をこれで包む。
// pty の結合のテストが、拡張機能の goroutine（標準エラー出力の記録）の中で panic を起こし、端末が戻ることを確かめるのに使う。
var wrapLogHandler func(slog.Handler) slog.Handler

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		// -h と --help は、使い方を表示したうえで正常終了とする。
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "modal-editor:", err)
		os.Exit(1)
	}
}

// run は、コマンドライン引数を解釈して処理を実行する。
// テストから呼び出せるように、引数と入出力を受け取る形にしている。
// エディタを動かすのは、stdin と stdout がどちらも端末の *os.File のときだけである。
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modal-editor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(flags.Output(), "使い方: modal-editor [オプション] [ファイル ...]") // 使い方を書けなくても続ける
		flags.PrintDefaults()
	}
	showVersion := flags.Bool("version", false, "バージョンを表示して終了する")
	logPath := flags.String("log", "", "ログを追記するファイル。指定しなければログを書かない（画面を崩さないため、標準エラー出力には書かない）")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("引数の解釈に失敗: %w", err)
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}

	in, inOK := stdin.(*os.File)
	out, outOK := stdout.(*os.File)
	if !inOK || !outOK {
		return errNotTerminal
	}
	return edit(flags.Args(), in, out, *logPath)
}

var errNotTerminal = errors.New("標準入力と標準出力が端末のときだけ起動できる")

// edit は、端末を開いてエディタを動かし、終わったら端末を元に戻す。
//
// 端末は、次のどの終わり方でも元に戻す（基本設計 8 章）。
//
//   - :q などで Run が戻ったとき
//   - SIGTERM、SIGHUP、SIGINT を受けたとき（Run の ctx を取り消し、Run が戻ってから戻す）。
//     シグナルの受け付けは端末を開く前に始めるので、起動の途中（設定とファイルの読み込み）に受けても、
//     既定の動作で終わらずに、読み込みを終えてから Run が直ちに戻り、端末を戻す
//   - 本体の goroutine で panic したとき（その goroutine の上で戻してから panic を続ける。#114）。
//     この goroutine（イベントループ）は guard、internal/term と terminal の goroutine は term.Terminal.RestoreOnPanic、
//     internal/editor と internal/exthost（拡張機能との接続の internal/rpc を含む）の goroutine は editor.Config.OnPanic で戻す
//
// Go の panic は他の goroutine の recover で捕まえられないので、戻すのは panic した goroutine 自身である。
// 標準ライブラリの関数だけを呼ぶ goroutine（signal.NotifyContext の中のもの、exthost の子プロセスの回収の cmd.Wait、
// exthost の context.AfterFunc が取り消しの関数を呼ぶもの）の panic と、
// SIGKILL で終わったときは戻せない。
func edit(files []string, in, out *os.File, logPath string) (err error) {
	logger, closeLog, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	if wrapLogHandler != nil {
		logger = slog.New(wrapLogHandler(logger.Handler()))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer stop()
	tm, err := term.Open(in, out, term.Options{})
	if errors.Is(err, term.ErrNotTerminal) {
		return errNotTerminal
	}
	if err != nil {
		return fmt.Errorf("端末を開けない: %w", err)
	}
	defer func() { _ = tm.Close() }() // Close は誤りを返さない
	return guard(tm.Restore, func() error {
		return runEditor(ctx, files, tm, logger)
	})
}

// guard は、f を実行し、f が戻るか panic したら restore を呼ぶ。
// panic は restore の後にそのまま続ける（スタックトレースが、元に戻した端末に出る）。
// f の誤りを、restore の誤りより優先して返す。
func guard(restore func() error, f func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			_ = restore() // panic を続けるので、戻す誤りは捨てる
			panic(p)
		}
	}()
	err = f()
	if rerr := restore(); rerr != nil && err == nil {
		err = fmt.Errorf("端末を戻せない: %w", rerr)
	}
	return err
}

// runEditor は、エディタを作って files を開き、Run が戻るまで動かす。
// Run が戻ったら、端末を戻してから拡張機能を止める（editor.Editor.Close の doc の順序）。
// ctx はシグナルで取り消される。起動の途中で取り消されていれば、Run は直ちに戻る。
func runEditor(ctx context.Context, files []string, tm *term.Terminal, logger *slog.Logger) error {
	cfg := loadConfig(logger)
	// editor と exthost が起こす goroutine の panic でも、その goroutine の上で端末を戻してから panic を続ける（基本設計 8 章）。
	cfg.OnPanic = func() { _ = tm.Restore() } // panic を続けるので、戻す誤りは捨てる
	ed, err := editor.New(newTerminal(tm), cfg)
	if err != nil {
		return fmt.Errorf("エディタを作れない: %w", err)
	}
	// Vim と同じく、引数の順にバッファの番号（:ls、:b N）を付け、最初のファイルをカーソルのあるバッファにする。
	// Open は開いたバッファへ移るので、最後に最初のファイルをもう一度開いて移る（開いているファイルは開き直さない）。
	order := files
	if len(files) > 1 {
		order = append(slices.Clone(files), files[0])
	}
	for _, f := range order {
		if err := ed.Open(f); err != nil {
			closeEditor(ed, logger)
			return fmt.Errorf("%s を開けない: %w", f, err)
		}
	}

	runErr := ed.Run(ctx)
	// 拡張機能の終了は数秒かかることがあるので、先に端末を戻す（editor.Editor.Close の doc）。
	if err := tm.Restore(); err != nil {
		logger.Error("端末を戻せない", "err", err)
	}
	closeEditor(ed, logger)

	switch {
	case runErr == nil:
		return nil
	case errors.Is(runErr, editor.ErrTerminalLost):
		return errors.New("端末の入力が閉じた")
	case ctx.Err() != nil:
		return fmt.Errorf("シグナルを受けて終了した: %w", context.Cause(ctx))
	}
	return runErr
}

// loadConfig は、設定ファイルと拡張機能の宣言ファイルを internal/config で読み、editor.Config に写す。
// 置き場所は config.DefaultPath と config.ExtensionDirs（どちらも仮）に従う。
// 誤り（位置と内容）は editor.Config.Problems に入れ、エディタが起動後に最初の 1 件を知らせる（基本設計 8 章）。
// 誤りのある項目は config が既定値のままにするので、誤りがあっても起動する。
// 設定の extensions.memoryLimitMb（FR-20）は、exthost.Config.MemoryLimitMB にそのまま写す（#107）。
func loadConfig(logger *slog.Logger) editor.Config {
	var problems []error
	var cfg config.Config
	if path, err := config.DefaultPath(); err != nil {
		problems = append(problems, fmt.Errorf("設定ファイルの場所を決められない: %w", err))
	} else {
		var errs []error
		cfg, errs = config.Load(path)
		problems = append(problems, errs...)
	}
	exe, err := os.Executable()
	if err != nil {
		logger.Warn("実行ファイルのパスを決められないので、同梱の拡張機能を探さない", "err", err)
		exe = ""
	}
	exts, errs := config.LoadExtensions(config.ExtensionDirs(exe), cfg)
	problems = append(problems, errs...)
	specs := make([]exthost.Spec, len(exts))
	for i, x := range exts {
		specs[i] = exthost.Spec(x)
	}
	for i, p := range problems {
		problems[i] = shortProblem(p)
	}
	host := exthost.Config{Logger: logger, MemoryLimitMB: cfg.MemoryLimitMB}
	// 設定ファイルの checkIntervalMs の 0 は「定期的に確かめない」で、editor.Config.CheckInterval では負の値に当たる。
	var interval time.Duration
	if d := cfg.CheckInterval; d != nil {
		interval = *d
		if interval == 0 {
			interval = -1
		}
	}
	return editor.Config{
		AutoRead:         cfg.AutoRead,
		CheckInterval:    interval,
		Keymap:           cfg.Keymap,
		SyncTimeout:      cfg.SyncTimeout,
		StartSyncTimeout: cfg.StartSyncTimeout,
		FileTypes:        cfg.FileTypes,
		FixEndOfLine:     cfg.FixEndOfLine,
		Extensions:       specs,
		Host:             host,
		Logger:           logger,
		Problems:         problems,
	}
}

// shortProblem は、画面の 1 行に出す設定の誤りの、ファイルのパスを短くする。
// 設定ファイルはファイル名だけ（config.jsonc）、宣言ファイルは拡張機能のディレクトリの名前とファイル名（line/manifest.json）にする。
// 絶対パスのままだと、80 桁の端末では位置と内容がコマンドラインの行の幅で切れて見えない。
// 置き場所は config.DefaultPath と config.ExtensionDirs で決まるので、名前だけで利用者が辿れる。
func shortProblem(err error) error {
	ce, ok := err.(*config.Error) //nolint:errorlint // 包んだ誤りは、包んだ文言を保つために短くしない
	if !ok || ce.Path == "" {
		return err
	}
	short := *ce
	short.Path = filepath.Base(ce.Path)
	if short.Path == config.ManifestFileName {
		short.Path = filepath.Join(filepath.Base(filepath.Dir(ce.Path)), short.Path)
	}
	return &short
}

func closeEditor(ed *editor.Editor, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if err := ed.Close(ctx); err != nil {
		logger.Error("拡張機能の終了に失敗", "err", err)
	}
}

// openLog は、path へ追記するロガーを返す。path が空なら、何も書かないロガーを返す。
func openLog(path string) (*slog.Logger, func(), error) {
	if path == "" {
		return slog.New(slog.DiscardHandler), func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("ログのファイルを開けない: %w", err)
	}
	return slog.New(slog.NewTextHandler(f, nil)), func() { _ = f.Close() }, nil // 閉じる誤りは記録する先が無い
}

// terminal は、term.Terminal を editor.Terminal の口に合わせる。
// term は editor と同じ層にあり editor を import しないので、イベントの型の変換をここで行う。
type terminal struct {
	*term.Terminal
	events chan editor.TermEvent
}

func newTerminal(tm *term.Terminal) *terminal {
	t := &terminal{Terminal: tm, events: make(chan editor.TermEvent)}
	go func() {
		defer close(t.events)
		defer tm.RestoreOnPanic()
		for ev := range tm.Input() {
			t.events <- editor.TermEvent(ev)
		}
	}()
	return t
}

// Input は、term.Terminal のイベントを editor.TermEvent にして送るチャネルを返す。
func (t *terminal) Input() <-chan editor.TermEvent { return t.events }

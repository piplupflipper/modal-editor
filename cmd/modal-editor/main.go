// Command modal-editor は、エディタ本体の実行ファイルである。
//
// 引数のファイルを開き、端末（標準入力と標準出力）でエディタを動かす。
// 編集の状態を持つ server（internal/server）と、端末を持つ編集の client（internal/client）を 1 つのプロセスの中で組み立て、
// 両者を internal/protocol の接続でつなぐ（基本設計 1.2。FR-60 の構成）。
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

	"github.com/piplupflipper/modal-editor/internal/client"
	"github.com/piplupflipper/modal-editor/internal/command"
	"github.com/piplupflipper/modal-editor/internal/config"
	"github.com/piplupflipper/modal-editor/internal/editor"
	"github.com/piplupflipper/modal-editor/internal/exthost"
	"github.com/piplupflipper/modal-editor/internal/protocol"
	"github.com/piplupflipper/modal-editor/internal/server"
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
	// req: FR-119
	readOnly := flags.Bool("R", false, "引数のファイルを、編集を禁止した状態で開く（:readonly で解ける）")

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
	return edit(flags.Args(), *readOnly, in, out, *logPath)
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
//     この goroutine（イベントループ）は guard、internal/term の goroutine と client を動かす goroutine は term.Terminal.RestoreOnPanic、
//     client がキーを送る goroutine は internal/client の中の recover、
//     internal/editor、internal/server、internal/exthost（拡張機能との接続の internal/rpc を含む）の goroutine は editor.Config.OnPanic で戻す
//
// Go の panic は他の goroutine の recover で捕まえられないので、戻すのは panic した goroutine 自身である。
// 標準ライブラリの関数だけを呼ぶ goroutine（signal.NotifyContext の中のもの、exthost の子プロセスの回収の cmd.Wait、
// exthost の context.AfterFunc が取り消しの関数を呼ぶもの）の panic と、
// SIGKILL で終わったときは戻せない。
// readOnly は起動のオプション -R で、files を編集を禁止した状態で開く（editor.Config.ReadOnlyArgs）。
func edit(files []string, readOnly bool, in, out *os.File, logPath string) (err error) {
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
		return runEditor(ctx, files, readOnly, tm, logger)
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

// runEditor は、server と編集の client を 1 つのプロセスの中で組み立て（FR-60 の構成。基本設計 1.2）、files を開いて、server の Run が戻るまで動かす。
// server と client の間は protocol.Pipe でつなぐ。server の Run はこの goroutine で、client の Run は別の goroutine で動かす。
// server の Run が戻ると接続が閉じ、client が端末を戻す。それを待ってから拡張機能を止める（editor.Editor.Close の doc の順序）。
// ctx はシグナルで取り消される。起動の途中で取り消されていれば、Run は直ちに戻る。
func runEditor(ctx context.Context, files []string, readOnly bool, tm *term.Terminal, logger *slog.Logger) error {
	cfg := loadConfig(logger)
	cfg.ReadOnlyArgs = readOnly
	// editor、exthost、server が起こす goroutine の panic でも、その goroutine の上で端末を戻してから panic を続ける（基本設計 8 章）。
	// 端末を持つのは client だが、同じプロセスの中なので server の側からも戻せる。
	cfg.OnPanic = func() { _ = tm.Restore() } // panic を続けるので、戻す誤りは捨てる
	srv, err := server.New(cfg)
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
		if err := srv.Open(f); err != nil {
			closeServer(srv, logger)
			return fmt.Errorf("%s を開けない: %w", f, err)
		}
	}

	clientConn, serverConn := protocol.Pipe()
	clientDone := make(chan error, 1)
	go func() {
		defer tm.RestoreOnPanic()
		clientDone <- client.Run(tm, clientConn, logger)
	}()
	runErr := srv.Run(ctx, serverConn)
	// 拡張機能の終了は数秒かかることがあるので、先に client に端末を戻させる（editor.Editor.Close の doc）。
	if err := <-clientDone; err != nil {
		logger.Error("端末を戻せない", "err", err)
	}
	closeServer(srv, logger)

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
		Leader:           cfg.Leader,
		SyncTimeout:      cfg.SyncTimeout,
		StartSyncTimeout: cfg.StartSyncTimeout,
		FileTypes:        cfg.FileTypes,
		FixEndOfLine:     cfg.FixEndOfLine,
		ReadOnlyPaths:    cfg.ReadOnlyPaths,
		Registers: command.RegisterConfig{
			HistorySize:     cfg.RegisterHistorySize,
			MaxEntryBytes:   cfg.RegisterMaxEntryBytes,
			DeleteToHistory: cfg.DeleteToHistory,
		},
		NoClipboard: cfg.RegisterClipboard == config.ClipboardOff,
		Extensions:  specs,
		Host:        host,
		Logger:      logger,
		Problems:    problems,
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

func closeServer(srv *server.Server, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	if err := srv.Close(ctx); err != nil {
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

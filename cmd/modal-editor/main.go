// Command modal-editor は、エディタ本体の実行ファイルである。
//
// 引数のファイルを開き、端末（標準入力と標準出力）でエディタを動かす。
// 編集の状態を持つのは server（internal/server）で、端末を持つのは編集の client（internal/client）である（基本設計 1.2）。
//
// tmux と herdr の中では、server を window（タブ）ごとに別のプロセスで動かす（FR-59）。
// 起動したらソケットのパスを決め（autostart.go の connectAuto）、そのパスの server に編集の client として繋ぐ。
// server がいなければ、自分の子として -server で起動してから繋ぐ。
// それ以外（FR-60）では、server と編集の client を 1 つのプロセスの中で組み立て、両者を internal/protocol の接続でつなぐ。
// このときも server のソケットを待ち受ける。
//
// 要件定義 8.1 の応答の計測のために、server と編集の client を別のプロセスで動かすオプションを持つ（利用者向けではない）。
// -listen <パス> は server だけを起動し、-connect <パス> はそのパスの server に編集の client として繋ぐ。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/piplupflipper/modal-editor/internal/mux"
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
	listen := flags.String("listen", "", "計測用（利用者向けではない）。端末を使わずに server だけを起動し、このパスのソケットで編集の client を待つ")
	connect := flags.String("connect", "", "計測用（利用者向けではない）。このパスのソケットの server に編集の client として繋ぐ。ファイルの引数は取らない")
	serverPath := flags.String("server", "", "内部用（利用者は指定しない）。tmux と herdr の中で起動した編集の client が、このパスのソケットの server として自分の子に起動する")
	// req: FR-84
	newServer := flags.Bool("new", false, "環境変数 "+envSocket+" があっても親の server に繋がず、新しい server と client を起動する（エディタの中で起動したエディタを、別のエディタとして使う）")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("引数の解釈に失敗: %w", err)
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}
	if sock := os.Getenv(envSocket); sock != "" && !*newServer && countSet(*listen, *connect, *serverPath) == 0 {
		// :! などの子孫として起動した modal-editor は、待つだけの client になる（FR-83）。
		// MODAL_EDITOR_SOCKET の server がもう無ければ、規則 2 以降でソケットを決める編集の client として動く（基本設計 1.2）。
		if conn, err := protocol.Dial(sock); err == nil {
			return waitClient(conn, os.Getenv, flags.Args(), *readOnly)
		}
	}
	switch {
	case countSet(*listen, *connect, *serverPath) > 1:
		return errors.New("-listen、-connect、-server は同時に指定できない")
	case *connect != "" && flags.NArg() > 0:
		return errors.New("-connect ではファイルを指定できない（開くのは server の側）")
	case *listen != "":
		return serve(*listen, flags.Args(), *readOnly, *logPath)
	case *serverPath != "":
		if flags.NArg() > 0 || *readOnly {
			return errors.New("-server ではファイルと -R を指定できない（繋いだ client が送る）")
		}
		return serveAuto(*serverPath, *logPath)
	}

	in, inOK := stdin.(*os.File)
	out, outOK := stdout.(*os.File)
	if !inOK || !outOK {
		return errNotTerminal
	}
	return edit(flags.Args(), *readOnly, in, out, *logPath, *connect, *newServer)
}

var errNotTerminal = errors.New("標準入力と標準出力が端末のときだけ起動できる")

// countSet は、values のうち空でないものの数を返す。
func countSet(values ...string) int {
	n := 0
	for _, v := range values {
		if v != "" {
			n++
		}
	}
	return n
}

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
// connect が空でなければ、server を動かさずに、そのパスの server に編集の client として繋ぐ（runClient）。
// newServer（--new。FR-84）なら、端末多重化ソフトの中でも同じ window の server に繋がず、1 つのプロセスの構成で新しい server と client を動かす
// （server のソケットは、プロセス ID と乱数の名前のディレクトリに置く。基本設計 4.9）。
//
// 端末を子プロセスへ渡している間（:!。FR-80）は、端末の <C-c> と <C-\> が本体にも SIGINT と SIGQUIT で届くので、
// その間は internal/client が signal.Ignore で無視にし、終わったら signalContext の受け取りを登録し直す（term.Terminal.SetInterruptRestore）。
func edit(files []string, readOnly bool, in, out *os.File, logPath, connect string, newServer bool) (err error) {
	logger, closeLog, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	if wrapLogHandler != nil {
		logger = slog.New(wrapLogHandler(logger.Handler()))
	}

	ctx, stop, renotify := signalContext()
	defer stop()
	tm, err := term.Open(in, out, term.Options{})
	if errors.Is(err, term.ErrNotTerminal) {
		return errNotTerminal
	}
	if err != nil {
		return fmt.Errorf("端末を開けない: %w", err)
	}
	tm.SetInterruptRestore(renotify)
	defer func() { _ = tm.Close() }() // Close は誤りを返さない
	return guard(tm.Restore, func() error {
		if connect != "" {
			conn, err := protocol.Dial(connect)
			if err != nil {
				return fmt.Errorf("server に繋げない: %w", err)
			}
			return runClient(ctx, conn, tm, logger)
		}
		if newServer {
			// req: FR-84
			return runEditor(ctx, files, readOnly, tm, logger, nil)
		}
		start := func(path string) (*serverChild, error) {
			return startServerProcess(path, startOptions{logPath: logPath})
		}
		conn, problem := connectAuto(ctx, os.Getenv, mux.Detect(os.Getenv), start, logger)
		if conn != nil {
			var first protocol.ServerMessage
			first, problem = startClient(conn, tm, files, readOnly)
			if problem == nil {
				return continueClient(ctx, conn, first, tm, logger)
			}
			_ = conn.Close() // 使わない接続。閉じる誤りは扱えない
		}
		if problem != nil {
			logger.Warn("server を使えない", "err", problem)
		}
		return runEditor(ctx, files, readOnly, tm, logger, problem)
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
// problem が nil でなければ、端末多重化ソフトの中なのに server を使えなかった理由で、起動後のメッセージで知らせる（基本設計 1.2）。
//
// req: FR-60
func runEditor(ctx context.Context, files []string, readOnly bool, tm *term.Terminal, logger *slog.Logger, problem error) error {
	cfg := loadConfig(logger)
	cfg.ReadOnlyArgs = readOnly
	if problem != nil {
		cfg.Problems = append(cfg.Problems, problem)
	}
	// editor、exthost、server が起こす goroutine の panic でも、その goroutine の上で端末を戻してから panic を続ける（基本設計 8 章）。
	// 端末を持つのは client だが、同じプロセスの中なので server の側からも戻せる。
	cfg.OnPanic = func() { _ = tm.Restore() } // panic を続けるので、戻す誤りは捨てる
	// server のソケットは、MCP のソケットと同じディレクトリに置く（基本設計 1.2 の `<名前>/`）。
	// 1 つのプロセスの構成の `<名前>` は、MCP のソケットと同じくプロセス ID と乱数から作る（FR-60）。
	sock, err := exthost.NewMCPSocket()
	if err != nil {
		logger.Warn("server と MCP のソケットのディレクトリを作れない", "err", err)
		sock = nil
	} else {
		cfg.MCPSocket = sock.Path
		cfg = withChildEnv(cfg, filepath.Join(filepath.Dir(sock.Path), server.SocketName), logger)
	}
	defer func() {
		if rerr := sock.Remove(); rerr != nil { // 拡張機能を止めた後（closeServer の後）に消す
			logger.Warn("ソケットのディレクトリを消せない", "err", rerr)
		}
	}()
	srv, err := server.New(cfg)
	if err != nil {
		return fmt.Errorf("エディタを作れない: %w", err)
	}
	if err = openFiles(srv, files); err != nil {
		closeServer(srv, logger)
		return err
	}
	// 1 つのプロセスの構成でも server のソケットを待ち受ける（FR-60。基本設計 1.2）。待ち受けられなくても編集は続ける。
	var ln net.Listener
	if sock != nil {
		if ln, err = server.ListenInOwnDir(filepath.Join(filepath.Dir(sock.Path), server.SocketName)); err != nil {
			logger.Warn("server のソケットで待ち受けられない", "err", err)
			ln = nil
		}
	}

	clientConn, serverConn := protocol.Pipe()
	clientDone := make(chan error, 1)
	go func() {
		defer tm.RestoreOnPanic()
		clientDone <- client.Run(tm, clientConn, logger)
	}()
	runErr := srv.Serve(ctx, ln, serverConn)
	// 拡張機能の終了は数秒かかることがあるので、先に client に端末を戻させる（editor.Editor.Close の doc）。
	// server の終わり方（client.ErrServerLost など）は、同じプロセスの server の runErr で知らせるので、ここでは端末を戻す誤りだけを見る。
	var exitErr *client.ServerExitError
	if err := <-clientDone; err != nil && !errors.Is(err, client.ErrServerLost) && !errors.As(err, &exitErr) {
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

// openFiles は、files を srv で開く。
// Vim と同じく、引数の順にバッファの番号（:ls、:b N）を付け、最初のファイルをカーソルのあるバッファにする。
// Open は開いたバッファへ移るので、最後に最初のファイルをもう一度開いて移る（開いているファイルは開き直さない）。
func openFiles(srv *server.Server, files []string) error {
	order := files
	if len(files) > 1 {
		order = append(slices.Clone(files), files[0])
	}
	for _, f := range order {
		if err := srv.Open(f); err != nil {
			return fmt.Errorf("%s を開けない: %w", f, err)
		}
	}
	return nil
}

// serve は、-listen のときの本体で、server だけを動かす（基本設計 1.2 の計測のためのオプション。利用者向けではない）。
// 端末を使わない。path のソケットで待ち受け、files を開き、最初に繋いだ編集の client の終わり（:q か、接続が切れた）かシグナルで終わる。
func serve(path string, files []string, readOnly bool, logPath string) error {
	logger, closeLog, err := openLog(logPath)
	if err != nil {
		return err
	}
	defer closeLog()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	defer stop()

	cfg := withChildEnv(loadConfig(logger), path, logger)
	cfg.ReadOnlyArgs = readOnly
	srv, err := server.New(cfg)
	if err != nil {
		return fmt.Errorf("エディタを作れない: %w", err)
	}
	defer closeServer(srv, logger)
	if err = openFiles(srv, files); err != nil {
		return err
	}
	ln, err := server.Listen(path)
	if err != nil {
		return fmt.Errorf("server のソケットで待ち受けられない: %w", err)
	}
	err = srv.Serve(ctx, ln, nil)
	switch {
	case err == nil, errors.Is(err, editor.ErrTerminalLost):
		// 最後の編集の client が終わったら、server も終わる（FR-61）
		return nil
	case ctx.Err() != nil:
		return fmt.Errorf("シグナルを受けて終了した: %w", context.Cause(ctx))
	}
	return err
}

// runClient は、server のソケットの接続 conn で、編集の client として動く（基本設計 1.2）。
// tmux と herdr の中で server に繋いだときと、-connect（計測のためのオプション。利用者向けではない）のときに使う。
// server が接続を閉じるか、端末の入力が終わるまで動かす。ctx が終わったら接続を閉じ、端末を戻して終わる。
func runClient(ctx context.Context, conn protocol.ClientConn, tm *term.Terminal, logger *slog.Logger) error {
	return clientUntil(ctx, conn, func() error { return client.Run(tm, conn, logger) })
}

// continueClient は、startClient で最初のメッセージ first を受けた conn で、runClient と同じく編集の client として動く。
func continueClient(ctx context.Context, conn protocol.ClientConn, first protocol.ServerMessage, tm *term.Terminal, logger *slog.Logger) error {
	return clientUntil(ctx, conn, func() error { return client.Continue(tm, conn, first, logger) })
}

// startClient は、tmux と herdr の中で繋いだ server へ、起動の引数のファイル（絶対パスにしたもの）と -R とペインと作業ディレクトリを
// 載せた Hello を送り（clientHello）、最初のメッセージを返す（client.Start）。server は、この client を繋いだ後にファイルを開く。
// 同じ server に別の編集の client が繋いでいても、この client を足す（FR-62）。
//
// 最初のメッセージの前に接続が切れたときは、problem に理由を入れて返す。呼び出し側は、それを知らせて 1 つのプロセスで動かす。
// 理由付きの終わりの知らせ（ファイルを開けないなど）は first として返し、client.Continue がその理由で終わる。
func startClient(conn protocol.ClientConn, tm *term.Terminal, files []string, readOnly bool) (first protocol.ServerMessage, problem error) {
	abs := make([]string, len(files))
	for i, f := range files {
		a, err := filepath.Abs(f)
		if err != nil {
			return nil, fmt.Errorf("%s の絶対パスを決められないので 1 つのプロセスで動かす: %w", f, err)
		}
		abs[i] = a
	}
	w, h := tm.Size()
	hello := clientHello(os.Getenv, abs, readOnly, w, h)
	hello.Features = client.Features(tm)
	first, err := client.Start(conn, hello)
	if err != nil {
		return nil, fmt.Errorf("server に繋げないので 1 つのプロセスで動かす: %w", err)
	}
	return first, nil
}

// clientUntil は、編集の client として動く run を、ctx が終わったら conn を閉じる形で動かす。
func clientUntil(ctx context.Context, conn protocol.ClientConn, run func() error) error {
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() }) // client.Run が端末を戻して戻る
	defer stopClose()
	if err := run(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return fmt.Errorf("シグナルを受けて終了した: %w", context.Cause(ctx))
	}
	return nil
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
		errs = append(errs, cfg.CheckAppearance(appearanceCheck)...)
		config.SortErrors(errs)
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
		Colors:           uiColors(cfg.Colors),
		Symbols:          cfg.Symbols,
		StatusLine:       uiStatusLine(cfg.StatusLine),
		TabLine:          uiTabLine(cfg.TabLineShow),
		TabLineParts:     uiStatusLine(cfg.TabLineParts),
		Registers: command.RegisterConfig{
			HistorySize:     cfg.RegisterHistorySize,
			MaxEntryBytes:   cfg.RegisterMaxEntryBytes,
			DeleteToHistory: cfg.DeleteToHistory,
		},
		NoClipboard:    cfg.RegisterClipboard == config.ClipboardOff,
		ElevateCommand: cfg.ElevateCommand,
		Extensions:     specs,
		Host:           host,
		Logger:         logger,
		Problems:       problems,
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

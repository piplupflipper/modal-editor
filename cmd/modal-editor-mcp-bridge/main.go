// Command modal-editor-mcp-bridge は、MCP のクライアントと起動中のエディタの間を中継する実行ファイルである。
//
// MCP のクライアント（コーディングエージェント）は、このコマンドを標準入出力の MCP サーバーとして起動する。
// このコマンドは起動中のエディタの Unix ドメインソケットへ接続し、
// 標準入力からソケットへ、ソケットから標準出力へ、バイト列をそのまま転送する。
// メッセージの内容は解釈しない（基本設計 6.2）。
//
// 接続先のソケットは、次の順で決める。
//
//  1. `--socket <path>` で指定されたパス
//  2. 環境変数 MODAL_EDITOR_MCP_SOCKET（仮の名前）のパス
//
// どちらも無いとき、または接続できないときは、理由を標準エラー出力に書いて終了コード 1 で終わる。
// 起動中のエディタを中継コマンドが見つける規則は要件定義 7 章で未決なので、ここでは裁定に依存しない最小の規則だけを持つ。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/piplupflipper/modal-editor/internal/version"
)

// socketEnv は、接続先のソケットのパスを渡す環境変数の名前である（仮）。
// エディタが子プロセスへこの変数を渡すと、その子孫の中継コマンドは起動元のエディタへ繋がる。
const socketEnv = "MODAL_EDITOR_MCP_SOCKET"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		// -h と --help は、使い方を表示したうえで正常終了とする。
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "modal-editor-mcp-bridge:", err)
		os.Exit(1)
	}
}

// run は、コマンドライン引数を解釈し、ソケットへ接続して中継する。
// テストから呼び出せるように、引数、標準入出力、環境変数の読み出しを受け取る形にしている。
// 中継が正常に終わったとき（どちらかの側が閉じたとき）は nil を返す。
//
// req: FR-33
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("modal-editor-mcp-bridge", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "バージョンを表示して終了する")
	socket := flags.String("socket", "", "接続先のエディタの Unix ドメインソケットのパス（省略時は環境変数 "+socketEnv+"）")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("引数の解釈に失敗: %w", err)
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("余分な引数がある: %v", flags.Args())
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}

	path := *socket
	if path == "" {
		path = getenv(socketEnv)
	}
	if path == "" {
		return fmt.Errorf("接続先のソケットが指定されていない（--socket か環境変数 %s で指定する）", socketEnv)
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fmt.Errorf("エディタのソケットへの接続に失敗: %w", err)
	}

	return bridge(conn, stdin, stdout)
}

// bridge は、stdin から conn へ、conn から stdout へ、バイト列をそのまま転送する。
//
// 終わり方は、どちらの向きが先に終わるかで分ける。
//
//   - stdin が EOF になったら、conn の書き込み側だけを閉じ（CloseWrite）、
//     conn からの読み取りが EOF になるまで stdout への転送を続けてから戻る。
//     エディタがまだ返していない応答（最後のリクエストへの応答）を失わないためである。
//     エディタは、読み取りが EOF になったら応答を書き終えてから接続を閉じる前提である
//   - conn からの読み取りが先に終わったら（エディタが閉じた、または誤り）、conn を閉じてすぐ戻る。
//     stdin を読んでいる向きは、stdin が閉じるまで終わらないので、待たない
//     （実行ファイルでは、戻った後にプロセスが終わる）
//   - stdin の読み取りや conn への書き込みが誤りで終わったら、conn を閉じて誤りを返す
//
// EOF と、conn を閉じたことによる誤りは、正常な終わりとして nil を返す。
//
// req: FR-33
func bridge(conn *net.UnixConn, stdin io.Reader, stdout io.Writer) error {
	upDone := make(chan error, 1)
	downDone := make(chan error, 1)

	go func() {
		_, err := io.Copy(conn, stdin)
		if err != nil {
			upDone <- fmt.Errorf("標準入力からソケットへの転送に失敗: %w", err)
			return
		}
		if err := conn.CloseWrite(); err != nil {
			upDone <- fmt.Errorf("ソケットの書き込み側を閉じるのに失敗: %w", err)
			return
		}
		upDone <- nil
	}()
	go func() {
		_, err := io.Copy(stdout, conn)
		if err != nil {
			err = fmt.Errorf("ソケットから標準出力への転送に失敗: %w", err)
		}
		downDone <- err
	}()

	var err error
	select {
	case err = <-downDone:
	case err = <-upDone:
		if err == nil {
			// 標準入力は EOF になった。エディタが接続を閉じるまで応答の転送を続ける。
			err = <-downDone
		}
	}
	if cerr := conn.Close(); cerr != nil && err == nil {
		err = fmt.Errorf("ソケットを閉じるのに失敗: %w", cerr)
	}
	if errors.Is(err, net.ErrClosed) {
		// 相手の向きが先に閉じたことによる誤りは、正常な終わりとして扱う。
		return nil
	}
	return err
}

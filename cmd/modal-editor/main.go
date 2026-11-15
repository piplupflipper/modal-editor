// Command modal-editor は、エディタ本体の実行ファイルである。
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/piplupflipper/modal-editor/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		// -h と --help は、使い方を表示したうえで正常終了とする。
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "modal-editor:", err)
		os.Exit(1)
	}
}

// run は、コマンドライン引数を解釈して処理を実行する。
// テストから呼び出せるように、引数と出力先を受け取る形にしている。
func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("modal-editor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	showVersion := flags.Bool("version", false, "バージョンを表示して終了する")

	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("引数の解釈に失敗: %w", err)
	}

	if *showVersion {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}

	return errors.New("エディタ本体は未実装（--version だけが動作する）")
}

// Command reqtrace は、要件定義の ID とコードの注釈を突き合わせて、進捗の一覧を生成する。
//
// 使い方:
//
//	go run ./cmd/reqtrace          # docs/progress.md を生成する
//	go run ./cmd/reqtrace -check   # 生成済みの docs/progress.md が最新かを検査する
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/piplupflipper/vi-imitation/internal/reqtrace"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "reqtrace:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("reqtrace", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "走査するリポジトリのルート")
	reqPath := flags.String("requirements", "docs/requirements.md", "要件定義のファイル（root からの相対パス）")
	outPath := flags.String("out", "docs/progress.md", "出力するファイル（root からの相対パス）")
	check := flags.Bool("check", false, "ファイルを書き換えず、出力が最新でなければ失敗する")
	if err := flags.Parse(args); err != nil {
		return err
	}

	reqFile, err := os.Open(filepath.Join(*root, *reqPath))
	if err != nil {
		return err
	}
	defer reqFile.Close() //nolint:errcheck // 読み込み専用で開いたファイルなので、Close の失敗で失うデータが無い

	reqs, err := reqtrace.ParseRequirements(reqFile)
	if err != nil {
		return err
	}
	if len(reqs) == 0 {
		return fmt.Errorf("%s に要件が 1 件も無い", *reqPath)
	}

	refs, err := reqtrace.Scan(*root)
	if err != nil {
		return err
	}

	report := reqtrace.Build(reqs, refs)
	if len(report.Unknown) > 0 {
		// 見つかった位置を全て 1 つのエラーにまとめて返す。
		errs := make([]error, len(report.Unknown))
		for i, ref := range report.Unknown {
			errs[i] = fmt.Errorf("%s:%d: 要件定義に無い ID: %s", ref.File, ref.Line, ref.ID)
		}
		return errors.Join(errs...)
	}

	// 出力するファイルからルートへ戻る相対パスを、リンクの先頭に付ける。
	prefix, err := filepath.Rel(filepath.Dir(*outPath), ".")
	if err != nil {
		return err
	}
	want := reqtrace.Render(report, filepath.ToSlash(prefix)+"/")

	out := filepath.Join(*root, *outPath)
	if *check {
		got, err := os.ReadFile(out)
		if err != nil {
			return fmt.Errorf("%w（make progress で生成する）", err)
		}
		if string(got) != want {
			return fmt.Errorf("%s が最新ではない（make progress で再生成する）", *outPath)
		}
		return nil
	}

	//nolint:gosec // リポジトリに置く文書なので、他の利用者も読める権限にする
	return os.WriteFile(out, []byte(want), 0o644)
}

package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/piplupflipper/vi-imitation/docs"
)

// runDocs は、--docs の引数 args に従って、本体に埋め込んだ文書を w へ出す（FR-172。基本設計 7.2）。
//
//   - 引数なし: 話題の一覧と使い方
//   - <話題>: その話題の文書の全体
//   - <話題> <節>: その話題の文書の、見出しが <節> の節だけ（docs.Section）
//
// 端末を使わないので、標準出力をパイプやファイルにしてよい（AI エージェントが読む）。
// 知らない話題と節、3 つ以上の引数は誤りにする。
//
// req: FR-172
func runDocs(args []string, w io.Writer) error {
	if len(args) == 0 {
		return writeTopics(w)
	}
	if len(args) > 2 {
		return errors.New("--docs の引数は <話題> と <節> の 2 つまでである")
	}
	t, ok := docs.LookupTopic(args[0])
	if !ok {
		return fmt.Errorf("知らない話題 %q（話題の一覧は vi-imitation --docs で出す）", args[0])
	}
	text := t.Text
	if len(args) == 2 {
		if text, ok = docs.Section(t.Text, args[1]); !ok {
			return fmt.Errorf("話題 %s に見出しが %q の節は無い（見出しの一覧は vi-imitation --docs %s | grep '^#' で出す）", t.Name, args[1], t.Name)
		}
	}
	_, err := io.WriteString(w, text)
	return err
}

// writeTopics は、話題の一覧（名前、大きさ、説明）と使い方を w へ出す。
func writeTopics(w io.Writer) error {
	var sb strings.Builder
	sb.WriteString("vi-imitation に埋め込んだ文書の話題の一覧である。\n")
	sb.WriteString("vi-imitation --docs <話題> で話題の文書の全体を、vi-imitation --docs <話題> <節> で見出しが <節> の節だけを、標準出力に出す。\n")
	sb.WriteString("節の名前は、見出しの # の後の文字列である（` は省いてよい）。見出しの一覧は vi-imitation --docs <話題> | grep '^#' で出す。\n\n")
	sb.WriteString("| 話題 | バイト数 | 内容 |\n|---|---|---|\n")
	for _, t := range docs.Topics() {
		fmt.Fprintf(&sb, "| %s | %d | %s |\n", t.Name, len(t.Text), t.Summary)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

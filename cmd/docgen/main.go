// Command docgen は、本体に埋め込む AI エージェント向けの文書（docs/agent/*.md）の一覧の部分を、コードの表から生成する（FR-172。基本設計 7.2）。
//
// 使い方:
//
//	go run ./cmd/docgen          # docs/agent/*.md の印の間を生成し直す（make docs、go generate ./docs）
//	go run ./cmd/docgen -check   # ファイルを書き換えず、印の間が最新でなければ失敗する（make docs-check）
//
// 文書の中の、次の 2 行の印に挟まれた部分を、名前ごとの生成の関数（blocks）の出力で置き換える。
// 印の外は人が書く。印の間は手で編集しない。
//
//	<!-- generated:<名前>:begin -->
//	<!-- generated:<名前>:end -->
//
// 一覧は、設定の表（option.Defs）、設定ファイルの最上位の項目（config.TopLevelNames）、操作の表（input.Actions）と既定のキー、
// キーの表記（editcmd）、Ex コマンドの表（ex.Names と editor.HostCommandNames）、拡張機能の期限の既定値（exthost の Default で始まる定数）、
// 拡張機能 API（ext と ext/server のソース）、スクリプトの拡張機能が yaegi に渡す標準ライブラリの記号の表（yaegi の stdlib と unrestricted）から作る。
// 機能を足す PR がこれらの表を変えて文書を生成し直し忘れると、make check（docs-check と、このパッケージのテスト）が失敗する。
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// agentDir は、生成の対象の文書を置くディレクトリの、リポジトリのルートからの相対パスである。
const agentDir = "docs/agent"

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "docgen:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("docgen", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "リポジトリのルート")
	check := flags.Bool("check", false, "ファイルを書き換えず、生成した一覧が最新でなければ失敗する")
	if err := flags.Parse(args); err != nil {
		return err
	}
	gen, err := newGenerator(*root)
	if err != nil {
		return err
	}
	files, err := filepath.Glob(filepath.Join(*root, agentDir, "*.md"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("%s に文書が無い", agentDir)
	}
	var stale []string
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := fill(string(data), gen.block)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if out == string(data) {
			continue
		}
		if *check {
			stale = append(stale, filepath.ToSlash(path))
			continue
		}
		if err := os.WriteFile(path, []byte(out), 0o644); err != nil { //nolint:gosec // リポジトリの文書なので、他の利用者が読めてよい
			return err
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("生成した一覧がコードの表とずれている（make docs で生成し直す）: %s", strings.Join(stale, " "))
	}
	return nil
}

// markerRE は、生成する部分の始まりと終わりの印の行である。
var markerRE = regexp.MustCompile(`^<!-- generated:([a-z][a-z-]*):(begin|end) -->$`)

// fill は、文書 text の印に挟まれた部分を、gen(名前) の出力で置き換えて返す。
// 始まりの無い終わり、終わりの無い始まり、入れ子の印、知らない名前（gen の誤り）は誤りにする。
func fill(text string, gen func(name string) (string, error)) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	var out strings.Builder
	open := "" // 開いている印の名前。空なら印の外
	for i, l := range lines {
		m := markerRE.FindStringSubmatch(strings.TrimSuffix(l, "\n"))
		switch {
		case m == nil && open == "":
			out.WriteString(l)
		case m == nil:
			// 印の間の古い中身は捨てる
		case m[2] == "begin" && open != "":
			return "", fmt.Errorf("%d 行目: %s の印の中で %s が始まる", i+1, open, m[1])
		case m[2] == "begin":
			body, err := gen(m[1])
			if err != nil {
				return "", fmt.Errorf("%d 行目: %w", i+1, err)
			}
			out.WriteString(l)
			out.WriteString(body)
			open = m[1]
		case m[1] != open:
			return "", fmt.Errorf("%d 行目: 始まりの無い %s の終わり", i+1, m[1])
		default:
			out.WriteString(l)
			open = ""
		}
	}
	if open != "" {
		return "", fmt.Errorf("%s の印に終わりが無い", open)
	}
	return out.String(), nil
}

// generator は、印の名前ごとに一覧を作る。
type generator struct {
	api  *extAPI
	root string
}

func newGenerator(root string) (*generator, error) {
	api, err := parseExtAPI(filepath.Join(root, "ext"))
	if err != nil {
		return nil, err
	}
	return &generator{api: api, root: root}, nil
}

// block は、印の名前 name の一覧を作る。出力は空でなければ改行で終わる。
func (g *generator) block(name string) (string, error) {
	var buf bytes.Buffer
	var err error
	switch name {
	case "key-names":
		err = writeKeyNames(&buf)
	case "keymap-modes":
		err = writeKeymapModes(&buf)
	case "lifecycle":
		writeLifecycle(&buf)
	case "ext-package":
		err = g.api.writePackageDoc(&buf)
	case "script-packages":
		writeScriptPackages(&buf)
	case "script-symbols":
		writeScriptSymbols(&buf)
	case "server-api":
		err = writeServerAPI(&buf, g.api, filepath.Join(g.root, "ext", "server"))
	case "options":
		writeOptions(&buf)
	case "config-keys":
		writeConfigKeys(&buf)
	case "actions":
		err = writeActions(&buf)
	case "ex-commands":
		writeExCommands(&buf)
	case "host-commands":
		writeHostCommands(&buf)
	case "provider-commands":
		g.api.writeOperations(&buf)
	case "api-version":
		g.api.writeVersion(&buf)
	case "env":
		g.api.writeEnv(&buf)
	case "manifest":
		g.api.writeManifest(&buf)
	case "methods":
		err = g.api.writeMethods(&buf)
	case "types":
		err = g.api.writeMessageTypes(&buf)
	default:
		return "", fmt.Errorf("知らない印の名前 %q", name)
	}
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

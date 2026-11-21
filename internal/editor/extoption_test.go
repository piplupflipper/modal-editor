package editor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// optionScript は、本体の設定を option/set で変え、option/get で読むスクリプトの拡張機能である（テスト用。#349）。
// スクリプトの拡張機能（yaegi）から呼べること（cmd/vi-imitation-ext-script の生成した表）も、これで確かめる。
//
//   - op.set: 引数は "<バッファ> <options の JSON>"。バッファが cur ならカーソルのあるバッファ、0 ならエディタ全体の項目を変える。
//     結果を message/show で "set ok" か "set err=<誤り>" と出す
//   - op.setft: 引数は "<バッファ> <ファイルの種類> <options の JSON>"。op.set と同じで、FileType を付けて送る
//   - op.get: 引数は "<バッファ> <名前>..."。結果を "get <values の JSON>" か "get err=<誤り>" と出す
//   - op.crash: 拡張機能を異常で終わらせる
const optionScript = `package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

func arg(p *ext.CommandExecuteParams) (ext.BufferID, string) {
	var a struct{ Text string ` + "`json:\"text\"`" + ` }
	_ = json.Unmarshal(p.Args, &a)
	b, rest, _ := strings.Cut(a.Text, " ")
	if b == "cur" {
		return p.Buffer, rest
	}
	n, _ := strconv.ParseUint(b, 10, 64)
	return ext.BufferID(n), rest
}

func main() {
	s := server.New()
	ctx := context.Background()
	s.Command("op.set", func(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		buf, rest := arg(p)
		var opts map[string]json.RawMessage
		_ = json.Unmarshal([]byte(rest), &opts)
		go func() {
			if err := s.SetOptions(ctx, ext.OptionSetParams{Buffer: buf, Options: opts}); err != nil {
				_ = s.ShowMessage(ext.MessageInfo, "set err="+err.Error())
				return
			}
			_ = s.ShowMessage(ext.MessageInfo, "set ok")
		}()
		return ext.CommandExecuteResult{OK: true}, nil
	})
	s.Command("op.setft", func(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		buf, rest := arg(p)
		ft, rest, _ := strings.Cut(rest, " ")
		var opts map[string]json.RawMessage
		_ = json.Unmarshal([]byte(rest), &opts)
		go func() {
			if err := s.SetOptions(ctx, ext.OptionSetParams{Buffer: buf, Options: opts, FileType: ft}); err != nil {
				_ = s.ShowMessage(ext.MessageInfo, "set err="+err.Error())
				return
			}
			_ = s.ShowMessage(ext.MessageInfo, "set ok")
		}()
		return ext.CommandExecuteResult{OK: true}, nil
	})
	s.Command("op.get", func(_ context.Context, _ *server.Buffer, p *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		buf, rest := arg(p)
		go func() {
			r, err := s.GetOptions(ctx, ext.OptionGetParams{Buffer: buf, Names: strings.Fields(rest)})
			if err != nil {
				_ = s.ShowMessage(ext.MessageInfo, "get err="+err.Error())
				return
			}
			b, _ := json.Marshal(r.Values)
			_ = s.ShowMessage(ext.MessageInfo, "get "+string(b))
		}()
		return ext.CommandExecuteResult{OK: true}, nil
	})
	s.Command("op.crash", func(_ context.Context, _ *server.Buffer, _ *ext.CommandExecuteParams) (ext.CommandExecuteResult, error) {
		os.Exit(3)
		return ext.CommandExecuteResult{}, nil
	})
	if err := s.Run(context.Background()); err != nil {
		log.Fatal(err)
	}
}
`

// buildOptionScript は、スクリプトの拡張機能の実行ファイルをビルドし、optionScript を置いて、ID が op の拡張機能の定義を返す。
func buildOptionScript(t *testing.T) exthost.Spec {
	t.Helper()
	if testing.Short() {
		t.Skip("拡張機能のビルドが要る")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "vi-imitation-ext-script")
	//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "vi-imitation-ext-script"), "./"+filepath.ToSlash(src))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "op.go"), []byte(optionScript), 0o600); err != nil {
		t.Fatal(err)
	}
	act := func(id string) ext.Activation { return ext.Activation{On: ext.ActivationCommand, ID: id} }
	m := ext.Manifest{
		ID: "op", Command: "./vi-imitation-ext-script", Args: []string{"op.go"},
		Activation:  []ext.Activation{act("op.set"), act("op.setft"), act("op.get"), act("op.crash")},
		Contributes: ext.Contributes{Commands: []ext.Contribution{{ID: "op.set"}, {ID: "op.setft"}, {ID: "op.get"}, {ID: "op.crash"}}},
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir}
}

// 拡張機能が option/set で入れたバッファごとの項目の値は、.editorconfig の後の段に入り、利用者の :set には勝たない。
// null で取り下げる。表に無い名前、filetype、範囲の合わない項目、型の違う値、範囲の外の値は誤りで断り、どの値も入れない。
// エディタ全体の項目は、設定ファイルの値に勝ち、:set には勝たない。
// 拡張機能が異常で終わったら、その拡張機能の値をすべて取り下げる。option/get は、本体が使う今の値を返す。
//
// req: FR-190, FR-191, FR-192
func TestExtensionOptionSet(t *testing.T) {
	spec := buildOptionScript(t)
	dir := writeFiles(t, map[string]string{
		".editorconfig": "root = true\n[*]\nindent_style = space\nindent_size = 2\n",
		"a.txt":         "x\n",
	})
	h := start(t, Config{
		WorkDir: dir, Extensions: []exthost.Spec{spec},
		Options: map[string]option.Value{option.ScrollMargin: option.IntValue(3)},
	}, "a.txt")
	h.settle()
	run := func(cmd, want string) {
		t.Helper()
		h.settle()
		h.e.do(func() { h.e.message.Text = "" }) // 前の結果と同じ文言を待ち違えないように消す
		h.keys(":" + cmd + "<CR>")
		h.eventually(cmd+" の結果 "+want, func() bool { return strings.Contains(h.e.message.Text, want) })
	}
	indent := func(what, want string) {
		t.Helper()
		h.e.do(func() {
			if got := indentValues(h.e.cur); got != want {
				t.Errorf("%s: %s, want %s", what, got, want)
			}
		})
	}
	editorInt := func(what, name string, want int) {
		t.Helper()
		h.e.do(func() {
			if got := h.e.options.Get(name).Int(); got != want {
				t.Errorf("%s: %s = %d, want %d", what, name, got, want)
			}
		})
	}
	indent("開いたとき", "true 2 2")

	run(`op.set cur {"indent.width":4,"tab.width":3}`, "set ok")
	indent("拡張機能の値", "true 4 3")
	run(`op.get cur indent.width tab.width indent.useSpaces`, `get {"indent.useSpaces":true,"indent.width":4,"tab.width":3}`)

	h.keys(":set sw=5<CR>")
	run(`op.set cur {"indent.width":6}`, "set ok")
	indent(":set の後に拡張機能が送り直した", "true 5 3")

	for _, c := range []struct{ cmd, want string }{
		{`op.set cur {"tab.width":7,"no.such":1}`, `unknown option "no.such"`},
		{`op.set cur {"filetype":"go"}`, "filetype is detected"},
		{`op.set cur {"scroll.margin":1}`, "scroll.margin is an editor-wide option"},
		{`op.set 0 {"tab.width":7}`, "tab.width is a buffer option"},
		{`op.set cur {"tab.width":7,"indent.width":-1}`, "indent.width: option: value is out of range"},
		{`op.set cur {"tab.width":"7"}`, "tab.width: option: value must be a decimal integer"},
		{`op.set cur {"indent.useSpaces":1}`, "indent.useSpaces: option: value must be true or false"},
		{`op.set cur {"comment.string":"#"}`, "comment.string: option: value must be empty or contain"},
		{`op.set 99 {"tab.width":7}`, "unknown buffer 99"},
		{`op.get 0 tab.width`, "tab.width is a buffer option"},
		{`op.get cur nope`, `unknown option "nope"`},
	} {
		run(c.cmd, c.want)
	}
	indent("誤りの後（どの値も入れない）", "true 5 3")

	run(`op.set cur {"tab.width":null}`, "set ok")
	indent("tab.width を取り下げた", "true 5 2")

	run(`op.set 0 {"scroll.margin":7,"lineWrap.enabled":true}`, "set ok")
	editorInt("エディタ全体の項目", option.ScrollMargin, 7)
	run(`op.get 0 scroll.margin lineWrap.enabled`, `get {"lineWrap.enabled":true,"scroll.margin":7}`)
	h.keys(":set nowrap<CR>")
	run(`op.set 0 {"lineWrap.enabled":true}`, "set ok")
	h.e.do(func() {
		if h.e.options.Get(option.LineWrapEnabled).Bool() {
			t.Error(":set nowrap の後に拡張機能の値が勝った")
		}
	})

	run(`op.set cur {"tab.width":4,"comment.string":"# %s"}`, "set ok")
	h.keys(":op.crash<CR>")
	h.eventually("異常で終わった後に拡張機能の値を取り下げる", func() bool {
		return indentValues(h.e.cur) == "true 5 2" && h.e.options.Get(option.ScrollMargin).Int() == 3
	})
	h.e.do(func() {
		if got := h.e.cur.option(option.CommentString).String(); got != "" {
			t.Errorf("取り下げた後の comment.string = %q", got)
		}
		if h.e.options.Get(option.LineWrapEnabled).Bool() {
			t.Error("取り下げた後に :set nowrap の値が変わった")
		}
	})
}

// ファイルの種類が変わった後に、拡張機能が新しい種類の値として（FileType を付けて）送った最初の値は、本体のファイルの種類ごとの段と
// 同じく、その値を持つ項目の :set の値を隠す（PR #352 のレビューの推奨 9 へのオーケストレータの決め）。
// 2 つ目からの値（設定ファイルを保存した、など）、種類の合わない値、FileType の無い値、開いたときの値は隠さない。
// 変わった後、値が届く前に :set で変えた項目も隠さない。隠した項目も、後で :set で変えればその値を使う。
//
// req: FR-190
func TestExtensionOptionMask(t *testing.T) {
	spec := buildOptionScript(t)
	dir := writeFiles(t, map[string]string{"a.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, "a.txt")
	h.settle()
	run := func(cmd string) {
		t.Helper()
		h.settle()
		h.e.do(func() { h.e.message.Text = "" })
		h.keys(":" + cmd + "<CR>")
		h.eventually(cmd+" の結果", func() bool { return strings.Contains(h.e.message.Text, "set ok") })
	}
	indent := func(what, want string) {
		t.Helper()
		h.settle()
		h.e.do(func() {
			if got := indentValues(h.e.cur); got != want {
				t.Errorf("%s: %s, want %s", what, got, want)
			}
		})
	}
	h.keys(":set sw=3 et ts=5<CR>")
	run(`op.setft cur text {"indent.width":2}`)
	indent("開いたときの値は隠さない", "true 3 5")

	h.keys(":set filetype=python<CR>")
	indent(":set filetype=python の直後", "true 3 5")
	run(`op.setft cur python {"indent.width":2,"tab.width":2}`)
	indent("種類が変わった後の最初の値は :set の値を隠す", "true 2 2")
	run(`op.setft cur python {"indent.useSpaces":false,"indent.width":4}`)
	indent("2 つ目の値は新たに隠さない（隠した項目は新しい値になる）", "true 4 2")
	h.keys(":set sw=7<CR>")
	indent("隠した項目も :set で変えればその値", "true 7 2")

	h.keys(":set filetype=go<CR>")
	run(`op.setft cur python {"tab.width":3}`)
	indent("種類の合わない値は捨てる（go の本体の既定は useSpaces と width を隠す）", "false 4 5")
	h.e.do(func() {
		if got := h.e.cur.extOptions["op"][option.TabWidth]; got != option.IntValue(2) {
			t.Errorf("種類の合わない値を段に入れた: tab.width = %v, want 2（前の値のまま）", got)
		}
	})
	run(`op.setft cur go {"tab.width":3}`)
	indent("合う種類の最初の値は隠す", "false 4 3")

	h.keys(":set filetype=c<CR>:set ts=9<CR>")
	run(`op.setft cur c {"tab.width":2}`)
	indent("変わった後に :set で変えた項目は隠さない", "true 7 9")

	h.keys(":set filetype=lua<CR>")
	run(`op.set cur {"tab.width":2}`)
	indent("FileType の無い値は隠さない", "true 7 9")

	// 異常で終わって起動し直した後も、勝ち負けは同じ（起動し直した後の最初の値で隠し直す。PR #352 の再レビューの推奨 1）。
	h.keys(":set filetype=sh<CR>")
	run(`op.setft cur sh {"tab.width":2}`)
	indent("sh の最初の値で隠す", "true 7 2")
	h.keys(":op.crash<CR>")
	h.eventually("異常で終わった後に値を取り下げる", func() bool { return indentValues(h.e.cur) == "true 7 9" })
	h.eventually("起動し直す", func() bool {
		st, _ := h.e.host.State("op")
		return st == exthost.StateRunning || st == exthost.StateNotStarted
	})
	run(`op.setft cur sh {"tab.width":2}`)
	indent("起動し直した後の最初の値で隠し直す", "true 7 2")
}

// buildFormatter は、整形ツールの拡張機能（cmd/vi-imitation-ext-formatter）をビルドし、settings の設定で、
// fileTypes のファイルの種類で起動する定義を返す。
func buildFormatter(t *testing.T, settings string, fileTypes ...string) exthost.Spec {
	t.Helper()
	if testing.Short() {
		t.Skip("拡張機能のビルドが要る")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go が無い")
	}
	dir := t.TempDir()
	src := filepath.Join("..", "..", "cmd", "vi-imitation-ext-formatter")
	//nolint:gosec // 引数は定数と、テストが作った一時ディレクトリのパスだけである
	cmd := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "vi-imitation-ext-formatter"), "./"+filepath.ToSlash(src))
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build extension: %v\n%s", err, out)
	}
	m := ext.Manifest{ID: "formatter", Command: "./vi-imitation-ext-formatter"}
	for _, ft := range fileTypes {
		m.Activation = append(m.Activation, ext.Activation{On: ext.ActivationFileType, Pattern: ft})
	}
	if _, err := ext.ValidateManifests([]ext.Manifest{m}); err != nil {
		t.Fatal(err)
	}
	return exthost.Spec{Manifest: m, Dir: dir, Settings: json.RawMessage(settings)}
}

// 整形ツールの拡張機能と結合して、ファイルの種類を A→B→A と続けて変えても、毎回 A の整形ツールの値が :set の値を隠す。
// 拡張機能が 2 つの buffer/open を 1 回の読み直しにまとめても、種類が変わったバッファには値が同じでも送り直すためである
// （PR #352 の再レビューの必須 1。前は 30 回中 14 回、隠さなかった）。
//
// req: FR-190, FR-193
func TestFormatterIndentRetype(t *testing.T) {
	spec := buildFormatter(t, `{"formatters": [
		{"command": "ruff", "fileTypes": ["python"]},
		{"command": "biome", "fileTypes": ["javascript"]}
	]}`, "python", "javascript")
	dir := writeFiles(t, map[string]string{"pyproject.toml": "[tool.ruff]\nindent-width = 2\n", "a.py": "x = 1\n"})
	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, "a.py")
	h.eventually("ruff の幅", func() bool { return indentValues(h.e.cur) == "false 2 2" })
	for i := range 20 {
		h.keys(":set sw=3<CR>")
		h.eventually("sw=3", func() bool { return h.e.cur.option(option.IndentWidth).Int() == 3 })
		h.keys(":set filetype=javascript | set filetype=python<CR>")
		h.eventually(fmt.Sprintf("%d 回目: python に戻った後に ruff の幅が :set を隠す", i+1), func() bool {
			return h.e.cur.fileType() == "python" && indentValues(h.e.cur) == "false 2 2"
		})
	}
}

// 複数の拡張機能が同じ項目に値を入れたら、宣言ファイルを先に読んだ拡張機能の値が勝つ。届いた順には依らない。
// 拡張機能の段はファイルの種類ごとの段ではないので、ファイルの種類が変わっても値を残し、:set の値を隠さない。
//
// req: FR-190
func TestExtensionOptionOrder(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "x\n"})
	h := start(t, Config{WorkDir: dir, Extensions: fakeSpecs(t)}, "a.txt")
	h.settle()
	// fakeSpecs の順は fake、ftx、slow である。
	h.e.do(func() {
		bs := h.e.cur
		bs.extOptions = mergeExtValues(bs.extOptions, "ftx", map[string]option.Value{option.IndentWidth: option.IntValue(3), option.TabWidth: option.IntValue(5)}, nil)
		bs.extOptions = mergeExtValues(bs.extOptions, "fake", map[string]option.Value{option.IndentWidth: option.IntValue(4)}, nil)
		h.e.refreshOptionLayers(bs)
		if got := indentValues(bs); got != "false 4 5" {
			t.Errorf("先に読んだ fake の値が勝たない: %s, want false 4 5", got)
		}
		bs.extOptions = mergeExtValues(bs.extOptions, "ftx", map[string]option.Value{option.IndentWidth: option.IntValue(6)}, nil)
		h.e.refreshOptionLayers(bs)
		if got := indentValues(bs); got != "false 4 5" {
			t.Errorf("後から届いた ftx の値が勝った: %s, want false 4 5", got)
		}
		bs.extOptions = mergeExtValues(bs.extOptions, "fake", nil, []string{option.IndentWidth})
		h.e.refreshOptionLayers(bs)
		if got := indentValues(bs); got != "false 6 5" {
			t.Errorf("fake が取り下げた後: %s, want false 6 5", got)
		}
		if _, ok := bs.extOptions["fake"]; ok {
			t.Error("値の無くなった拡張機能を残した")
		}
	})
	h.keys(":set ts=2<CR>:set filetype=go<CR>")
	h.settle()
	// go の本体の既定（indent.useSpaces false、indent.width 0）の後に拡張機能の段を当てる。tab.width の :set の値は隠さない。
	h.e.do(func() {
		if got := indentValues(h.e.cur); got != "false 6 2" {
			t.Errorf(":set filetype=go の後: %s, want false 6 2", got)
		}
	})
}

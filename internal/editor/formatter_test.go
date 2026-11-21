package editor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// formatterSpec は、標準の整形ツールの拡張機能を、formatters の設定 formatters で使う形にする。
// 設定のファイルの種類の契機の展開（internal/config の LoadExtensions）を、ここで写す。
func formatterSpec(t *testing.T, formatters []map[string]any) exthost.Spec {
	t.Helper()
	spec := buildStdExt(t, "vi-imitation-ext-format")
	raw, err := json.Marshal(map[string]any{"formatters": formatters, "localBinDirs": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	spec.Settings = raw
	for _, f := range formatters {
		for _, ft := range f["fileTypes"].([]string) {
			spec.Manifest.Activation = append(spec.Manifest.Activation, ext.Activation{On: ext.ActivationFileType, Pattern: ft})
		}
	}
	return spec
}

// formatterScript は、dir/name に sh のスクリプトを書き、そのパスを返す。
func formatterScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // テストの整形ツールは実行できるファイルにする
		t.Fatal(err)
	}
	return p
}

// 標準の整形ツールの拡張機能は、設定に書いたファイルの種類のファイルを開くと起動し、:w で書き出す前に整形ツールで整形する。
// 整形は変わった行だけの編集で、u 1 回で整形の前に戻る。
// gq{モーション} は、整形の結果のうち範囲の行と重なるところだけを適用する。
// 整形ツールが失敗しても書き出し、失敗を標準エラー出力の要旨と一緒に知らせる。
//
// req: FR-175, FR-176
func TestStandardFormatter(t *testing.T) {
	dir := t.TempDir()
	squeeze := formatterScript(t, dir, "squeeze", "tr -s ' '")
	broken := formatterScript(t, dir, "broken", "cat >/dev/null; echo 'line 1: bad input' >&2; exit 2")
	spec := formatterSpec(t, []map[string]any{
		{"command": squeeze, "fileTypes": []string{"python"}},
		{"name": "broken", "command": broken, "fileTypes": []string{"sh"}},
	})
	py := filepath.Join(dir, "a.py")
	sh := filepath.Join(dir, "b.sh")
	original := "keep\nx  =  1\nkeep\nkeep\ny  =  2\n"
	for _, p := range []string{py, sh} {
		if err := os.WriteFile(p, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	readFile := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}}, py)
	h.eventually("整形ツールの拡張機能の起動", func() bool { return h.state("format") == exthost.StateRunning })

	h.keys("3G:w<CR>")
	h.eventually("整形して書き出したファイル", func() bool { return readFile(py) == "keep\nx = 1\nkeep\nkeep\ny = 2\n" })
	h.settle()
	if got := h.text(); got != "keep\nx = 1\nkeep\nkeep\ny = 2" {
		t.Errorf("整形した本文 = %q", got)
	}
	// 変わっていない行のカーソルは、整形の後もその行に残る。
	if got := h.cursor(); got.Line != 2 {
		t.Errorf("整形の後のカーソル = %+v, want 保存の前の 2 行目", got)
	}
	h.keys("u")
	h.settle()
	if got := h.text(); got != strings.TrimSuffix(original, "\n") {
		t.Errorf("u の後の本文 = %q, want 整形の前", got)
	}
	// 変わった行の範囲（1 行目から 4 行目）に保存の前のカーソルがあるので、Vim の u と同じくその位置へ戻る（undoCursor）。
	if got := h.cursor(); got.Line != 2 {
		t.Errorf("u の後のカーソル = %+v, want 保存の前の 2 行目", got)
	}
	// 整形は 1 回のアンドゥの単位である。
	var canUndo bool
	h.e.do(func() { canUndo = h.e.cur.buf.CanUndo() })
	if canUndo {
		t.Error("整形を u 1 回で戻した後に、まだアンドゥの単位が残る")
	}

	// gqq は、カーソルの行と重なる差分の塊だけを適用する。
	h.keys("5Ggqq")
	h.eventually("5 行目だけの整形", func() bool { return h.e.cur.buf.Text() == "keep\nx  =  1\nkeep\nkeep\ny = 2" })

	// 失敗する整形ツールのファイルも書き出し、失敗を知らせる。
	h.keys(":e " + sh + "<CR>")
	h.eventually("b.sh のタブ", func() bool { return h.e.cur.path == sh })
	h.keys("ggihello <Esc>:w<CR>")
	h.eventually("整形せずに書き出したファイル", func() bool { return readFile(sh) == "hello "+original })
	h.eventually("失敗の知らせ", func() bool {
		return slices.ContainsFunc(h.e.msgHistory, func(m ui.Message) bool {
			return m.Text == "整形ツール broken が失敗した（終了コード 2）: line 1: bad input"
		})
	})
}

// 保存の流れで buffer/willSave の応答を待つ期限（extensions.willSaveTimeoutMs を写した exthost.Config.WillSaveTimeout）を超えた整形は、
// 待たずに整形せずに書き出し、そのことを知らせる。期限が整形にかかる時間より長ければ、整形してから書き出す。
//
// req: FR-177
func TestStandardFormatterWillSaveTimeout(t *testing.T) {
	dir := t.TempDir()
	slow := formatterScript(t, dir, "slow", "sleep 0.5; tr -s ' '")
	spec := formatterSpec(t, []map[string]any{{"command": slow, "fileTypes": []string{"python"}}})
	for _, c := range []struct {
		timeout time.Duration
		want    string
	}{
		{100 * time.Millisecond, "x  =  1\n"},
		{5 * time.Second, "x = 1\n"},
	} {
		py := filepath.Join(dir, "a.py")
		if err := os.WriteFile(py, []byte("x  =  1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		h := start(t, Config{WorkDir: dir, Extensions: []exthost.Spec{spec}, Host: exthost.Config{WillSaveTimeout: c.timeout}}, py)
		h.eventually("整形ツールの拡張機能の起動", func() bool { return h.state("format") == exthost.StateRunning })
		h.keys(":w<CR>")
		h.eventually("書き出し", func() bool {
			return strings.Contains(h.e.message.Text, "を書き込みました") || strings.Contains(h.e.message.Text, "保存の前の処理が失敗しました")
		})
		h.settle()
		got, err := os.ReadFile(py)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != c.want {
			t.Errorf("期限 %v: ファイル = %q, want %q（message=%q）", c.timeout, got, c.want, h.message())
		}
		if c.want == "x  =  1\n" && !strings.Contains(h.message(), "拡張機能 format の保存の前の処理が失敗しました") {
			t.Errorf("期限 %v: message = %q, want 期限を超えた知らせ", c.timeout, h.message())
		}
	}
}

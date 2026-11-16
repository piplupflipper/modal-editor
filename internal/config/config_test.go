package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
)

// problemsOf は、errs を *Error の列にする。*Error でないものがあればテストを失敗させる。
func problemsOf(t *testing.T, errs []error) []*Error {
	t.Helper()
	out := make([]*Error, 0, len(errs))
	for _, e := range errs {
		var p *Error
		if !errors.As(e, &p) {
			t.Fatalf("*Error でない誤り: %v", e)
		}
		out = append(out, p)
	}
	return out
}

// wantErrors は、errs の位置と文言の一部が want と一致することを確かめる。
func wantErrors(t *testing.T, errs []error, want []struct {
	line, col int
	msg       string
},
) {
	t.Helper()
	ps := problemsOf(t, errs)
	if len(ps) != len(want) {
		t.Fatalf("誤りの数 = %d, want %d: %v", len(ps), len(want), errs)
	}
	for i, w := range want {
		p := ps[i]
		if p.Line != w.line || p.Column != w.col || !strings.Contains(p.Msg, w.msg) {
			t.Errorf("誤り %d = %d:%d %q, want %d:%d に %q を含む", i, p.Line, p.Column, p.Msg, w.line, w.col, w.msg)
		}
	}
}

// コメントと末尾のカンマを含む設定ファイルを読める。
//
// req: FR-40
func TestParseJSONC(t *testing.T) {
	src := `// 設定ファイル
{
  /* キーマップ */
  "keymap": {
    "normal": { "<Space>w": ":w<CR>", },
    "insert": { "jk": "<Esc>" }, // 末尾のカンマ
  },
  "fileTypes": { ".tmpl": "html", "Justfile": "make", },
  "extensions": {
    "syncTimeoutMs": 50,
    "startSyncTimeoutMs": 2000,
    "memoryLimitMb": 256,
    "settings": { "lsp": { "servers": [ { "fileTypes": ["go"], }, ], }, },
    "activation": { "lsp": [ { "on": "filetype", "pattern": "python" }, ] },
  },
}
`
	cfg, errs := Parse("config.jsonc", []byte(src))
	if len(errs) != 0 {
		t.Fatalf("誤り: %v", errs)
	}
	wantKeymap := map[string]map[string]string{
		"normal": {"<Space>w": ":w<CR>"},
		"insert": {"jk": "<Esc>"},
	}
	if !reflect.DeepEqual(cfg.Keymap, wantKeymap) {
		t.Errorf("Keymap = %v, want %v", cfg.Keymap, wantKeymap)
	}
	if cfg.SyncTimeout == nil || *cfg.SyncTimeout != 50*time.Millisecond {
		t.Errorf("SyncTimeout = %v, want 50ms", cfg.SyncTimeout)
	}
	if cfg.StartSyncTimeout != 2*time.Second {
		t.Errorf("StartSyncTimeout = %v, want 2s", cfg.StartSyncTimeout)
	}
	if cfg.MemoryLimitMB != 256 {
		t.Errorf("MemoryLimitMB = %d, want 256", cfg.MemoryLimitMB)
	}
	if want := map[string]string{".tmpl": "html", "Justfile": "make"}; !reflect.DeepEqual(cfg.FileTypes, want) {
		t.Errorf("FileTypes = %v, want %v", cfg.FileTypes, want)
	}
	if got, want := string(cfg.ExtensionSettings["lsp"]), `{"servers":[{"fileTypes":["go"]}]}`; got != want {
		t.Errorf("ExtensionSettings[lsp] = %s, want %s", got, want)
	}
	if want := []ext.Activation{{On: ext.ActivationFileType, Pattern: "python"}}; !reflect.DeepEqual(cfg.ExtensionActivation["lsp"], want) {
		t.Errorf("ExtensionActivation[lsp] = %v, want %v", cfg.ExtensionActivation["lsp"], want)
	}
}

// syncTimeoutMs の 0 は「待たない」で、項目が無いときの nil（既定値）と区別する。
func TestParseFixEndOfLine(t *testing.T) {
	cases := []struct {
		src  string
		want *bool
		errs int
	}{
		{`{"fixendofline": false}`, new(false), 0},
		{`{"fixendofline": true}`, new(true), 0},
		{`{}`, nil, 0},
		{`{"fixendofline": "no"}`, nil, 1},
	}
	for _, c := range cases {
		cfg, errs := Parse("c", []byte(c.src))
		if len(errs) != c.errs || !reflect.DeepEqual(cfg.FixEndOfLine, c.want) {
			t.Errorf("%s: FixEndOfLine = %v, errs = %v; want %v, 誤り %d 件", c.src, cfg.FixEndOfLine, errs, c.want, c.errs)
		}
	}
	_, errs := Parse("c", []byte(`{"fixendofline": 0}`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{{1, 18, "fixendofline は true か false にする"}})
}

// autoread と checkIntervalMs は、項目が無いときの nil（editor の既定値）と、書いた値を区別する（#143）。
// checkIntervalMs の 0 は「定期的に確かめない」である。
func TestParseExternalChange(t *testing.T) {
	ms := func(n int) *time.Duration { d := time.Duration(n) * time.Millisecond; return &d }
	cases := []struct {
		src      string
		autoRead *bool
		interval *time.Duration
		errs     int
	}{
		{`{}`, nil, nil, 0},
		{`{"autoread": false, "checkIntervalMs": 0}`, new(false), ms(0), 0},
		{`{"autoread": true, "checkIntervalMs": 250}`, new(true), ms(250), 0},
		{`{"autoread": 1, "checkIntervalMs": -1}`, nil, nil, 2},
	}
	for _, c := range cases {
		cfg, errs := Parse("c", []byte(c.src))
		if len(errs) != c.errs || !reflect.DeepEqual(cfg.AutoRead, c.autoRead) || !reflect.DeepEqual(cfg.CheckInterval, c.interval) {
			t.Errorf("%s: AutoRead = %v, CheckInterval = %v, errs = %v", c.src, cfg.AutoRead, cfg.CheckInterval, errs)
		}
	}
}

// 空のファイル、空白とコメントだけのファイル、BOM 付きのファイルは誤りにしない。
//
// req: FR-40
func TestParseEmptyAndBOM(t *testing.T) {
	for _, src := range []string{"", "\n  \n", "// {\n//   \"keymap\": {}\n// }\n", "/* {} */", "// 改行なし", "\xef\xbb\xbf", "\xef\xbb\xbf// x\n"} {
		cfg, errs := Parse("c", []byte(src))
		if len(errs) != 0 || !reflect.DeepEqual(cfg, Config{}) {
			t.Errorf("%q: cfg = %+v, errs = %v; want ゼロ値で誤りなし", src, cfg, errs)
		}
	}
	// 閉じていないブロックコメントは構文の誤りのまま。
	if _, errs := Parse("c", []byte("/* {}")); len(errs) != 1 {
		t.Errorf("閉じていないコメント: errs = %v, want 1 件", errs)
	}
	// BOM の後の内容は読み、位置は BOM の後から数える。
	cfg, errs := Parse("c", []byte("\xef\xbb\xbf{\"keymap\": {\"normal\": {\"Y\": \"y$\"}}, \"x\": 1}"))
	if cfg.Keymap["normal"]["Y"] != "y$" {
		t.Errorf("Keymap = %v", cfg.Keymap)
	}
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{{1, 37, "未知の項目 x"}})
}

// 誤りの位置は、コメントを含む元のファイルで数える（複数行のコメントと多バイトのコメントの後）。
func TestParsePositionAfterComments(t *testing.T) {
	src := "{\n  /* c\n  c */ \"keymap\": 1,\n  // あいう\n  /* え */ \"extensions\": { /* お */ \"syncTimeoutMs\": -1 },\n}"
	_, errs := Parse("c", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{3, 18, "keymap はオブジェクトにする"},
		{5, 56, "extensions.syncTimeoutMs は 0 以上"},
	})
}

func TestParseSyncTimeoutZero(t *testing.T) {
	cfg, errs := Parse("c", []byte(`{"extensions": {"syncTimeoutMs": 0}}`))
	if len(errs) != 0 || cfg.SyncTimeout == nil || *cfg.SyncTimeout != 0 {
		t.Fatalf("SyncTimeout = %v, errs = %v; want 0", cfg.SyncTimeout, errs)
	}
	cfg, errs = Parse("c", []byte(`{}`))
	if len(errs) != 0 || cfg.SyncTimeout != nil {
		t.Fatalf("SyncTimeout = %v, errs = %v; want nil", cfg.SyncTimeout, errs)
	}
}

// 誤りのある項目は既定値のままにし、位置と内容を返す。他の項目は読む。
//
// req: FR-40
func TestParseInvalidItems(t *testing.T) {
	src := `{
  "keymap": {
    "normal": { "<Foo>": "x", "a": 1, "b": "<Bar", "c": "<Nope>", "<Space>w": ":w<CR>" },
    "operator": {},
  },
  "fileTypes": { ".x": "", ".y": "y" },
  "extensions": {
    "syncTimeoutMs": "50",
    "memoryLimitMb": -1,
    "activation": { "lsp": [ { "on": "operator", "id": "x" }, { "on": "filetype" }, { "on": "filetype", "pattern": "c", "extra": 1 }, { "on": "filetype", "pattern": "go" } ] },
    "tabstop": 4,
    "startSyncTimeoutMs": -5,
  },
  "unknown": true,
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{3, 17, `キーの列 "<Foo>" を読めない`},
		{3, 36, "keymap.normal.a は文字列にする"},
		{3, 57, `展開する列 "<Nope>" を読めない`},
		{4, 5, "未知のモード keymap.operator"},
		{6, 24, `fileTypes[".x"] のファイルの種類の名前が空`},
		{8, 22, "extensions.syncTimeoutMs は 0 以上"},
		{9, 22, "extensions.memoryLimitMb は 0 以上"},
		{10, 30, "filetype"},
		{10, 63, "filetype"},
		{10, 85, `契機を読めない: json: unknown field "extra"`},
		{11, 5, "未知の項目 extensions.tabstop"},
		{12, 27, "extensions.startSyncTimeoutMs は 0 以上"},
		{14, 3, "未知の項目 unknown"},
	})
	if cfg.StartSyncTimeout != 0 {
		t.Errorf("StartSyncTimeout = %v, want 0（既定値）", cfg.StartSyncTimeout)
	}
	if want := map[string]map[string]string{"normal": {"<Space>w": ":w<CR>", "b": "<Bar"}}; !reflect.DeepEqual(cfg.Keymap, want) {
		t.Errorf("Keymap = %v, want %v", cfg.Keymap, want)
	}
	if want := map[string]string{".y": "y"}; !reflect.DeepEqual(cfg.FileTypes, want) {
		t.Errorf("FileTypes = %v, want %v", cfg.FileTypes, want)
	}
	if cfg.SyncTimeout != nil || cfg.MemoryLimitMB != 0 {
		t.Errorf("SyncTimeout = %v, MemoryLimitMB = %d; want 既定値", cfg.SyncTimeout, cfg.MemoryLimitMB)
	}
	if want := []ext.Activation{{On: ext.ActivationFileType, Pattern: "go"}}; !reflect.DeepEqual(cfg.ExtensionActivation["lsp"], want) {
		t.Errorf("ExtensionActivation[lsp] = %v, want %v", cfg.ExtensionActivation["lsp"], want)
	}
}

// 項目の型が違うときは、その項目を既定値にする。
func TestParseWrongTypes(t *testing.T) {
	src := `{"keymap": [], "fileTypes": "x", "extensions": {"settings": 1, "activation": {"lsp": {}}}}`
	cfg, errs := Parse("c", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{1, 12, "keymap はオブジェクトにする"},
		{1, 29, "fileTypes はオブジェクトにする"},
		{1, 61, "extensions.settings はオブジェクトにする"},
		{1, 86, "extensions.activation.lsp は配列にする"},
	})
	if cfg.Keymap != nil || cfg.FileTypes != nil || cfg.ExtensionSettings != nil || len(cfg.ExtensionActivation) != 0 {
		t.Errorf("cfg = %+v, want 既定値", cfg)
	}
}

// 構文の誤りは、全体を既定値にして、位置を返す。
func TestParseSyntaxError(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
	}{
		{"閉じていない", "{\n  \"keymap\": {\n", 3, 1},
		{"値が無い", "{\n  \"a\": ,\n}", 2, 8},
		{"最上位が配列", "\n[1]", 2, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, errs := Parse("c", []byte(c.src))
			ps := problemsOf(t, errs)
			if len(ps) != 1 || ps[0].Line != c.line || ps[0].Column != c.col {
				t.Fatalf("誤り = %v, want %d:%d の 1 件", errs, c.line, c.col)
			}
			if !reflect.DeepEqual(cfg, Config{}) {
				t.Errorf("cfg = %+v, want ゼロ値", cfg)
			}
		})
	}
}

// 列はバイトで数える。
func TestErrorColumnBytes(t *testing.T) {
	_, errs := Parse("c", []byte("{\"あ\": 1, \"b\": 2}"))
	ps := problemsOf(t, errs)
	if len(ps) != 2 || ps[1].Column != 12 {
		t.Fatalf("誤り = %v, want 2 件目が 1:12", errs)
	}
	if got, want := ps[1].Error(), "c:1:12: 未知の項目 b を無視する"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// 同じ名前の項目が重なったら、後のものを使い、後の項目の位置を知らせる。
func TestParseDuplicate(t *testing.T) {
	src := `{
  "keymap": { "normal": { "x": "<Nope>" } },
  "keymap": { "visual": { "J": "5j", "J": "3j" } },
}`
	cfg, errs := Parse("c", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{3, 3, "項目 keymap が重なっている"},
		{3, 38, "項目 keymap.visual.J が重なっている"},
	})
	if want := map[string]map[string]string{"visual": {"J": "3j"}}; !reflect.DeepEqual(cfg.Keymap, want) {
		t.Errorf("Keymap = %v, want %v", cfg.Keymap, want)
	}
}

// 存在しないファイルは、誤りにせず既定値で動く。
func TestLoadMissing(t *testing.T) {
	cfg, errs := Load(filepath.Join(t.TempDir(), "none.jsonc"))
	if len(errs) != 0 || !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("cfg = %+v, errs = %v; want ゼロ値で誤りなし", cfg, errs)
	}
}

// 読めないファイル（ディレクトリ）は、位置の無い誤りにする。
func TestLoadUnreadable(t *testing.T) {
	dir := t.TempDir()
	_, errs := Load(dir)
	ps := problemsOf(t, errs)
	if len(ps) != 1 || ps[0].Line != 0 || ps[0].Error() != dir+": "+ps[0].Msg {
		t.Fatalf("誤り = %v, want 位置の無い 1 件", errs)
	}
}

func TestLoadFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(p, []byte("{\"keymap\": {\"normal\": {\"Y\": \"y$\",},},}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, errs := Load(p)
	if len(errs) != 0 || cfg.Keymap["normal"]["Y"] != "y$" {
		t.Fatalf("cfg = %+v, errs = %v", cfg, errs)
	}
}

func TestDefaultPath(t *testing.T) {
	cases := []struct {
		name, xdg, home, want string
		wantErr               bool
	}{
		{"XDG", "/x/conf", "/home/u", "/x/conf/modal-editor/config.jsonc", false},
		{"XDG が空", "", "/home/u", "/home/u/.config/modal-editor/config.jsonc", false},
		{"XDG が相対パス", "rel", "/home/u", "/home/u/.config/modal-editor/config.jsonc", false},
		{"どちらも無い", "", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", c.xdg)
			t.Setenv("HOME", c.home)
			got, err := DefaultPath()
			if (err != nil) != c.wantErr || got != c.want {
				t.Errorf("DefaultPath() = %q, %v; want %q, 誤り %v", got, err, c.want, c.wantErr)
			}
		})
	}
}

func TestExtensionDirs(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/x/data")
	t.Setenv("HOME", "/home/u")
	got := ExtensionDirs("/opt/me/modal-editor")
	// 利用者のディレクトリを、同梱のディレクトリより先にする。
	want := []string{"/x/data/modal-editor/extensions", "/opt/me/extensions"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtensionDirs = %v, want %v", got, want)
	}
	t.Setenv("XDG_DATA_HOME", "")
	got = ExtensionDirs("")
	want = []string{"/home/u/.local/share/modal-editor/extensions"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtensionDirs = %v, want %v", got, want)
	}
	t.Setenv("HOME", "")
	if got := ExtensionDirs(""); len(got) != 0 {
		t.Errorf("ExtensionDirs = %v, want 空", got)
	}
}

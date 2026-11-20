package config

import (
	"errors"
	"reflect"
	"testing"
)

// keymap の右辺は、文字列ならキーの列、オブジェクトなら操作として読む。オブジェクトの形の誤りは位置付きで知らせ、その項目だけを使わない。
//
// req: FR-126
func TestParseKeymapAction(t *testing.T) {
	src := `{
  "keymap": {
    "normal": { "<Space>w": ":w<CR>", "<C-a>": { "action": "line-start" } },
    "insert": { "<C-a>": { "action": "insert.line-start" }, "<C-e>": { "action": "x", "argz": 1 }, "<C-f>": {}, "<C-b>": { "action": 1 }, "<C-n>": { "action": "" } },
    "commandLine": { "<C-a>": { "action": "line-start" }, "<C-e>": "<End>" },
    "visual": { "<Foo>": { "action": "left" } },
  },
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{4, 87, "keymap.insert[\"<C-e>\"]: 未知の項目 argz がある"},
		{4, 109, "操作の ID（action）か Ex コマンド（command）が無い"},
		{4, 134, "keymap.insert[\"<C-b>\"].action は文字列にする"},
		{4, 160, "操作の ID が空"},
		{5, 31, "commandLine には操作もコマンドも書けない"},
		{6, 17, `キーの列 "<Foo>" を読めない`},
	})
	if want := map[string]map[string]string{
		"normal": {"<Space>w": ":w<CR>"}, "insert": {}, "commandLine": {"<C-e>": "<End>"}, "visual": {},
	}; !reflect.DeepEqual(cfg.Keymap, want) {
		t.Errorf("Keymap = %v, want %v", cfg.Keymap, want)
	}
	if want := map[string]map[string]string{
		"normal": {"<C-a>": "line-start"}, "insert": {"<C-a>": "insert.line-start"},
	}; !reflect.DeepEqual(cfg.KeymapActions, want) {
		t.Errorf("KeymapActions = %v, want %v", cfg.KeymapActions, want)
	}

	// 操作の ID とモードは CheckKeymapActions で確かめ、使えない項目を位置付きの誤りにして除く。
	errs = cfg.CheckKeymapActions(func(mode, _, id string) error {
		if mode == "normal" && id == "line-start" {
			return nil
		}
		return errors.New("使えない")
	})
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{4, 26, `keymap.insert["<C-a>"]: 操作 insert.line-start を使えない: 使えない`},
	})
	if want := map[string]map[string]string{"normal": {"<C-a>": "line-start"}, "insert": {}}; !reflect.DeepEqual(cfg.KeymapActions, want) {
		t.Errorf("CheckKeymapActions の後の KeymapActions = %v, want %v", cfg.KeymapActions, want)
	}
}

// keymap の右辺のオブジェクトに command（と args）を書くと、Ex コマンドの割り当てとして KeymapCommands に入る。
// action と command の両方、command の無い args、空の command、文字列でない値、commandLine に書いた項目は、
// 位置付きの誤りにしてその項目だけを使わない。名前は CheckKeymapCommands で確かめる。
//
// req: FR-154
func TestParseKeymapCommand(t *testing.T) {
	src := `{
  "keymap": {
    "normal": {
      "<C-l>": { "command": "search.clearHighlight" },
      "<Space>n": { "command": "option.toggle", "args": "lineNumber.show" },
      "<C-x>": { "action": "line-end", "command": "tab.new" },
      "<C-y>": { "action": "line-end", "args": "x" },
      "<C-z>": { "command": "" },
      "<C-q>": { "command": "tab.new", "args": 1 },
      "<C-r>": { "args": "x" },
    },
    "insert": { "<C-l>": { "command": "search.clearHighlight", "args": "" } },
    "commandLine": { "<C-l>": { "command": "search.clearHighlight" } },
  },
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{6, 16, `keymap.normal["<C-x>"]: action と command は一方だけを書く`},
		{7, 16, `keymap.normal["<C-y>"]: args は command と一緒に書く`},
		{8, 29, `keymap.normal["<C-z>"].command: コマンドの名前が空である`},
		{9, 48, `keymap.normal["<C-q>"].args は文字列にする`},
		{10, 16, `keymap.normal["<C-r>"]: args は command と一緒に書く`},
		{13, 31, "commandLine には操作もコマンドも書けない"},
	})
	want := map[string]map[string]KeymapCommand{
		"normal": {
			"<C-l>":    {Name: "search.clearHighlight"},
			"<Space>n": {Name: "option.toggle", Args: "lineNumber.show"},
		},
		"insert": {"<C-l>": {Name: "search.clearHighlight"}},
	}
	if !reflect.DeepEqual(cfg.KeymapCommands, want) {
		t.Errorf("KeymapCommands = %v, want %v", cfg.KeymapCommands, want)
	}
	if cfg.KeymapActions != nil {
		t.Errorf("KeymapActions = %v, want nil", cfg.KeymapActions)
	}

	// 名前とモードは CheckKeymapCommands で確かめ、使えない項目を位置付きの誤りにして除く。
	errs = cfg.CheckKeymapCommands(func(mode, _ string, _ KeymapCommand) error {
		if mode == "normal" {
			return nil
		}
		return errors.New("使えない")
	})
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{12, 26, `keymap.insert["<C-l>"]: コマンド search.clearHighlight を使えない: 使えない`},
	})
	if got := cfg.KeymapCommands["insert"]; len(got) != 0 {
		t.Errorf("CheckKeymapCommands の後の insert = %v", got)
	}
	if got := len(cfg.KeymapCommands["normal"]); got != 2 {
		t.Errorf("CheckKeymapCommands の後の normal は %d 件", got)
	}
}

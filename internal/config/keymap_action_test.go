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
    "insert": { "<C-a>": { "action": "insert.line-start" }, "<C-e>": { "action": "x", "args": 1 }, "<C-f>": {}, "<C-b>": { "action": 1 }, "<C-n>": { "action": "" } },
    "commandLine": { "<C-a>": { "action": "line-start" }, "<C-e>": "<End>" },
    "visual": { "<Foo>": { "action": "left" } },
  },
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{4, 87, "keymap.insert[\"<C-e>\"]: 未知の項目 args がある"},
		{4, 109, "操作の ID（action）が無い"},
		{4, 134, "keymap.insert[\"<C-b>\"].action は文字列にする"},
		{4, 160, "操作の ID が空"},
		{5, 31, "commandLine には操作を書けない"},
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

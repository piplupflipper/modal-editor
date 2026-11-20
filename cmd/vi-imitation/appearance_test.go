package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/command/ex"
	"github.com/piplupflipper/vi-imitation/internal/config"
	"github.com/piplupflipper/vi-imitation/internal/editor"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 同梱する設定の例（config.example.jsonc）は、誤り無く読め、見た目の項目がすべて描画に渡る。
// keymap.insert の Emacs 風のキーは、本体の操作の ID として使える。
// commandAliases の :tabedit（:tabe）と :tabnew は、拡張機能が無くても Ex コマンドの別名の表に足せる。
//
// req: FR-91, FR-92, FR-93, FR-126, FR-150
func TestConfigExample(t *testing.T) {
	data, err := os.ReadFile("../../config.example.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	cfg, errs := config.Parse("config.example.jsonc", data)
	errs = append(errs, cfg.CheckAppearance(appearanceCheck)...)
	errs = append(errs, cfg.LoadColorScheme(t.TempDir(), appearanceCheck)...)
	errs = append(errs, cfg.CheckKeymapActions(editor.KeymapActionCheck(cfg.Leader))...)
	aliases, addAlias := editor.CommandAliases(nil)
	errs = append(errs, cfg.CheckCommandAliases(addAlias)...)
	if len(errs) != 0 {
		t.Fatalf("設定の例の誤り: %v", errs)
	}
	if got, want := aliases.Added(), []ex.RenameAlias{{Name: "tabedit", Min: 4, Target: "tab.new"}, {Name: "tabnew", Min: 6, Target: "tab.new"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("commandAliases = %+v, want %+v", got, want)
	}
	// インサートモードの Emacs 風のキー（#257）。Ctrl-H は <BS> として届くので、<C-h> は書かない（PR #258 のレビューの推奨 4）。
	wantInsert := map[string]string{
		"<C-a>": "insert.line-start", "<C-e>": "insert.line-end", "<C-f>": "insert.right", "<C-b>": "insert.left",
		"<C-n>": "insert.down", "<C-p>": "insert.up",
		"<C-d>": "insert.delete-char", "<C-k>": "insert.delete-to-line-end",
		"<C-w>": "insert.delete-prev-word", "<C-u>": "insert.delete-to-line-start", // #260
	}
	if !reflect.DeepEqual(cfg.KeymapActions, map[string]map[string]string{"insert": wantInsert}) {
		t.Errorf("keymap の操作 = %v", cfg.KeymapActions)
	}
	if cfg.ColorScheme != "iceberg" || len(cfg.SchemeColors) == 0 {
		t.Errorf("colorscheme %q, 配色 %d 件", cfg.ColorScheme, len(cfg.SchemeColors))
	}
	colors := uiColors(cfg.Colors)
	if got, want := colors[ui.StyleNonText], (ui.Attr{Fg: ui.RGB(0x44, 0x4b, 0x71)}); got != want {
		t.Errorf("nontext = %+v, want %+v", got, want)
	}
	if len(colors) != len(cfg.Colors) || len(cfg.Symbols) == 0 || cfg.StatusLine == nil {
		t.Errorf("colors %d 件, symbols %v, statusline %+v", len(colors), cfg.Symbols, cfg.StatusLine)
	}
	if got := uiColor(config.Color{Kind: config.ColorIndexed, Index: 3}); got != ui.Indexed(3) {
		t.Errorf("uiColor(3) = %v", got)
	}
	// 例は、アイコン（FR-152）を出す設定を書く（Nerd Font の無い環境で切る場所を示すため）。
	if v, ok := cfg.Options["icons.show"]; !ok || !v.Bool() {
		t.Errorf("icons.show = %v %v", v, ok)
	}
	for show, want := range map[string]ui.TabLineMode{"": ui.TabLineAlways, config.TabLineAlways: ui.TabLineAlways, config.TabLineMultiple: ui.TabLineMultiple} {
		if got := uiTabLine(show); got != want {
			t.Errorf("uiTabLine(%q) = %v, want %v", show, got, want)
		}
	}
}

// 同梱の配色のすべての名前は、本体の色を付ける対象の名前（ui.IsStyleName）で、誤り無く読める。
//
// req: FR-125
func TestBundledColorSchemeNames(t *testing.T) {
	names := config.BundledColorSchemes()
	if len(names) == 0 {
		t.Fatal("同梱の配色が無い")
	}
	for _, name := range names {
		cfg := config.Config{ColorScheme: name}
		// 色の名前を確かめない検査で読み、残った名前を ui.IsStyleName で確かめる（appearanceCheck が除く前の全部の名前）。
		if errs := cfg.LoadColorScheme("", config.AppearanceCheck{}); len(errs) != 0 {
			t.Errorf("%s の誤り: %v", name, errs)
		}
		for n := range cfg.SchemeColors {
			if !ui.IsStyleName(n) {
				t.Errorf("%s の %s は本体の色の名前でない", name, n)
			}
		}
	}
}

// 設定の colorscheme で選んだ配色を editor.Config.ColorScheme に写し、設定の colors は別に写す（重ねるのは editor）。
// 設定ディレクトリの colors/<名前>.jsonc は同梱の同じ名前の配色より優先し、見つからない名前は位置付きの誤りにして既定の色で起動する。
//
// req: FR-125
func TestLoadConfigColorScheme(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	dir := filepath.Join(xdg, "config", "vi-imitation")
	if err := os.MkdirAll(filepath.Join(dir, config.ColorSchemeDir), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(p, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, p), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logger := slog.New(slog.DiscardHandler)

	write("config.jsonc", `{ "colorscheme": "iceberg", "colors": { "keyword": { "fg": 1 } } }`)
	cfg := loadConfig(logger)
	if len(cfg.Problems) != 0 {
		t.Fatalf("Problems = %v", cfg.Problems)
	}
	if got, want := cfg.ColorScheme[ui.StyleKeyword], (ui.Attr{Fg: ui.RGB(0x84, 0xa0, 0xc6)}); got != want {
		t.Errorf("配色の keyword = %+v, want %+v", got, want)
	}
	if got, want := cfg.Colors[ui.StyleKeyword], (ui.Attr{Fg: ui.Indexed(1)}); got != want || len(cfg.Colors) != 1 {
		t.Errorf("colors = %+v, want keyword だけ %+v", cfg.Colors, want)
	}

	write(filepath.Join(config.ColorSchemeDir, "iceberg.jsonc"), "{\n  \"comment\": { \"fg\": 2 },\n  \"nosuch\": {}\n}")
	cfg = loadConfig(logger)
	if want := (map[string]ui.Attr{ui.StyleComment: {Fg: ui.Indexed(2)}}); !reflect.DeepEqual(cfg.ColorScheme, want) {
		t.Errorf("利用者の iceberg = %+v, want %+v", cfg.ColorScheme, want)
	}
	if len(cfg.Problems) != 1 || !strings.HasPrefix(cfg.Problems[0].Error(), "colors/iceberg.jsonc:3:3: 配色 iceberg の nosuch は未知") {
		t.Errorf("Problems = %v", cfg.Problems)
	}

	write("config.jsonc", "{\n  \"colorscheme\": \"nosuch\"\n}")
	cfg = loadConfig(logger)
	if cfg.ColorScheme != nil {
		t.Errorf("見つからない配色で ColorScheme = %v", cfg.ColorScheme)
	}
	if len(cfg.Problems) != 1 || cfg.Problems[0].Error() != "config.jsonc:2:18: E185: 配色 nosuch が見つからない" {
		t.Errorf("Problems = %v", cfg.Problems)
	}
}

// 設定の icons のアイコンは、描画の表示幅の規則（ui.CheckIcon）で確かめ、使えるものだけを、文字と色を写して editor に渡す。
//
// req: FR-152
func TestIconsConfig(t *testing.T) {
	// U+F0868 は、サロゲートの組で書けば 1 つの文字として読む。\u の 4 桁で書き損じた "\uf0868" は、U+F086 と "8" の 2 文字なので
	// 誤りにする（PR #305 のレビューの推奨 3）。
	src := "{\n  \"icons\": { \"directory\": { \"text\": \"D\", \"fg\": 4, \"bold\": true },\n" +
		"    \"fileTypes\": { \"go\": { \"text\": \"wide\" }, \"dockerfile\": { \"text\": \"\\udb82\\udc68\" },\n" +
		"      \"make\": { \"text\": \"\\uf0868\" } } }\n}"
	cfg, errs := config.Parse("config.jsonc", []byte(src))
	errs = append(errs, cfg.CheckAppearance(appearanceCheck)...)
	if len(errs) != 2 || !strings.Contains(errs[0].Error(), "icons の fileTypes.go の text を使えない") ||
		!strings.Contains(errs[1].Error(), "icons の fileTypes.make の text を使えない: アイコンは 1 つの文字にする") {
		t.Errorf("errs = %v", errs)
	}
	want := ui.Icons{
		ui.IconDirectory:              {Text: "D", Attr: ui.Attr{Fg: ui.Indexed(4), Bold: true}},
		ui.IconFileType("dockerfile"): {Text: "\U000f0868"},
	}
	if got := uiIcons(cfg.Icons); !reflect.DeepEqual(got, want) {
		t.Errorf("uiIcons = %+v, want %+v", got, want)
	}
	if uiIcons(nil) != nil {
		t.Error("uiIcons(nil) != nil")
	}
}

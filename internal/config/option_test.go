package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/tailscale/hujson"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// testOptionDefs は、設定の表の代わりに使う項目である（3 段の入れ子、最上位の項目、バッファごとの文字列の項目を試すため）。
// 行番号の 2 つは、本体の表と同じ名前で範囲を外した項目にしてある（範囲の検査は TestParseLineNumberOptions で本体の表で試す）。
var testOptionDefs = []option.Def{
	{Name: "lineNumber.show", Type: option.Bool, Scope: option.ScopeEditor, Default: option.BoolValue(false)},
	{Name: "lineNumber.minWidth", Type: option.Int, Scope: option.ScopeEditor, Default: option.IntValue(4)},
	{Name: "a.b.c", Type: option.String, Scope: option.ScopeEditor, Default: option.StringValue("")},
	{Name: "top", Type: option.String, Scope: option.ScopeEditor, Default: option.StringValue("")},
	{Name: "buf.kind", Type: option.String, Scope: option.ScopeBuffer, Default: option.StringValue(""), Detected: true},
	{Name: "buf.width", Type: option.Int, Scope: option.ScopeBuffer, Default: option.IntValue(8)},
}

// parseOptions は、data の最上位の項目を、設定の表 defs の項目として読む（Parse の default の枝と同じ）。
func parseOptions(t *testing.T, data string, defs []option.Def) (Config, []error) {
	t.Helper()
	s := newSource("config.jsonc", []byte(data))
	root, err := s.parseJSONC()
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	for _, m := range s.members(root.Value.(*hujson.Object), "") {
		if !s.option(m, "", defs, &cfg) {
			s.report(m.nameOff, "未知の項目 %s を無視する", join("", m.name))
		}
	}
	return cfg, s.problems
}

// 設定の表の、範囲がエディタ全体の項目は、点で区切った名前を入れ子のキーにして書き、項目の型で読む。
//
// req: FR-131
func TestParseOptions(t *testing.T) {
	cfg, errs := parseOptions(t, `{
  "lineNumber": { "show": true, "minWidth": -2 },
  "a": { "b": { "c": "x y" } },
  "top": "t",
}`, testOptionDefs)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	want := map[string]option.Value{
		"lineNumber.show": option.BoolValue(true), "lineNumber.minWidth": option.IntValue(-2),
		"a.b.c": option.StringValue("x y"), "top": option.StringValue("t"),
	}
	if !reflect.DeepEqual(cfg.Options, want) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, want)
	}
	// 何も書かなければ nil で、表の既定値を使う。
	if cfg, errs := parseOptions(t, `{}`, testOptionDefs); cfg.Options != nil || len(errs) != 0 {
		t.Errorf("空の設定: Options = %+v, errs = %v", cfg.Options, errs)
	}
}

// 範囲がバッファごとの項目、型の違う値、表に無い名前は、位置付きの誤りとして知らせて使わない。他の項目は読む。
//
// req: FR-131
func TestParseOptionsErrors(t *testing.T) {
	cfg, errs := parseOptions(t, `{
  "lineNumber": { "show": 1, "minWidth": 1.5, "other": true },
  "buf": { "kind": "go" },
  "a": { "b": "c" },
  "top": "ok",
  "nosuch": 1
}`, testOptionDefs)
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{2, 27, "lineNumber.show は true か false にする"},
		{2, 42, "lineNumber.minWidth は整数にする"},
		{2, 47, "未知の項目 lineNumber.other を無視する"},
		{3, 12, "buf.kind はバッファを開くときに決まる設定なので、設定ファイルには書けない"},
		{4, 15, "a.b はオブジェクトにする"},
		{6, 3, "未知の項目 nosuch を無視する"},
	})
	if want := map[string]option.Value{"top": option.StringValue("ok")}; !reflect.DeepEqual(cfg.Options, want) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, want)
	}
}

// 本体の設定の表のどの項目も、Parse の既存の項目（keymap、tabline など）の読み方に吸われず、表の項目として読まれる。
// 表の項目の最上位の段が既存の項目の名前と重なると、既存の項目として読まれて、誤りも出ずに Options に入らない（基本設計 7.3）。
// 範囲がエディタ全体の項目は既定値を書いて Options に入ることを、バッファごとの項目は書けない誤りになることを確かめる。
//
// req: FR-131
func TestOptionDefsReadByParse(t *testing.T) {
	for _, d := range option.Defs() {
		val, err := json.Marshal(optionJSON(d.Default))
		if err != nil {
			t.Fatal(err)
		}
		src := string(val)
		parts := strings.Split(d.Name, ".")
		for i := len(parts) - 1; i >= 0; i-- {
			src = fmt.Sprintf("{%q: %s}", parts[i], src)
		}
		cfg, errs := Parse("config.jsonc", []byte(src))
		if !d.Detected {
			if len(errs) != 0 || cfg.Options[d.Name] != d.Default {
				t.Errorf("%s: Options = %+v, errs = %v（既存の項目と最上位の段が重なっていないか）", d.Name, cfg.Options, errs)
			}
		} else if len(errs) != 1 || !strings.Contains(errs[0].Error(), d.Name+" はバッファを開くときに決まる設定") {
			t.Errorf("%s: errs = %v（既存の項目と最上位の段が重なっていないか）", d.Name, errs)
		}
		if d.Scope != option.ScopeBuffer || d.Detected {
			continue
		}
		// 範囲がバッファごとの項目は、fileTypeOptions の下にも同じ形で書ける（FR-163）。
		cfg, errs = Parse("config.jsonc", []byte(`{"fileTypeOptions": {"go": `+src+`}}`))
		if len(errs) != 0 || cfg.FileTypeOptions["go"][d.Name] != d.Default {
			t.Errorf("%s: FileTypeOptions = %+v, errs = %v", d.Name, cfg.FileTypeOptions, errs)
		}
	}
}

// 範囲がバッファごとの項目は、最上位に書けばバッファを開くときの初めの値になり、fileTypeOptions の下にファイルの種類ごとにも書ける。
// fileTypeOptions には、範囲がエディタ全体の項目と、ファイルから判定して決める項目（filetype）は書けない。値の検査は最上位と同じである。
//
// req: FR-163
func TestParseFileTypeOptions(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{
  "indent": { "useSpaces": true, "width": 4 },
  "fileTypeOptions": {
    "go": { "indent": { "useSpaces": false }, "tab": { "width": 4 } },
    "make": { "lineNumber": { "show": true }, "filetype": "x", "indent": { "width": -1, "auto": true } },
    "": { "indent": { "auto": true } },
    "python": 1,
    "md": { "nosuch": 1 }
  }
}`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{5, 31, "lineNumber.show はエディタ全体の設定なので、ファイルの種類ごとには書けない"},
		{5, 47, "filetype はバッファを開くときに決まる設定なので、設定ファイルには書けない"},
		{5, 85, "indent.width は 0 から 9999 の整数にする: -1"},
		{6, 5, "fileTypeOptions のファイルの種類の名前が空"},
		{7, 15, "fileTypeOptions.python はオブジェクトにする"},
		{8, 13, "未知の項目 fileTypeOptions.md.nosuch を無視する"},
	})
	wantTop := map[string]option.Value{option.IndentUseSpaces: option.BoolValue(true), option.IndentWidth: option.IntValue(4)}
	if !reflect.DeepEqual(cfg.Options, wantTop) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, wantTop)
	}
	wantFT := map[string]map[string]option.Value{
		"go":   {option.IndentUseSpaces: option.BoolValue(false), option.TabWidth: option.IntValue(4)},
		"make": {option.IndentAuto: option.BoolValue(true)},
	}
	if !reflect.DeepEqual(cfg.FileTypeOptions, wantFT) {
		t.Errorf("FileTypeOptions = %+v, want %+v", cfg.FileTypeOptions, wantFT)
	}
}

// optionJSON は、v を JSON に書くときの Go の値にする。
func optionJSON(v option.Value) any {
	switch v.Type() {
	case option.Bool:
		return v.Bool()
	case option.Int:
		return v.Int()
	case option.String:
		return v.String()
	}
	return nil
}

// 本体の表の行番号の 2 つの項目は、"lineNumber": { "show": …, "minWidth": … } で書ける。
// minWidth の範囲（1 から 20）の外の値は、:option.set と同じ検査（option.Def.Check）で断り、位置付きの誤りとして知らせて既定値を使う。
//
// req: FR-135, FR-136
func TestParseLineNumberOptions(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{ "lineNumber": { "show": true, "minWidth": 5 } }`))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	want := map[string]option.Value{"lineNumber.show": option.BoolValue(true), "lineNumber.minWidth": option.IntValue(5)}
	if !reflect.DeepEqual(cfg.Options, want) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, want)
	}
	for _, tt := range []struct {
		src string
		col int
	}{
		{`{ "lineNumber": { "minWidth": 0 } }`, 31},
		{`{ "lineNumber": { "minWidth": -1 } }`, 31},
		{`{ "lineNumber": { "minWidth": 21 } }`, 31},
		// int に収まらない数も、範囲の外として知らせる（:set と同じ option.ParseInt で読む）。
		{`{ "lineNumber": { "minWidth": 99999999999999999999 } }`, 31},
		{`{ "lineNumber": { "minWidth": -99999999999999999999 } }`, 31},
	} {
		cfg, errs := Parse("config.jsonc", []byte(tt.src))
		val := strings.TrimSuffix(strings.TrimPrefix(tt.src, `{ "lineNumber": { "minWidth": `), ` } }`)
		wantErrors(t, errs, []struct {
			line, col int
			msg       string
		}{
			{1, tt.col, "lineNumber.minWidth は 1 から 20 の整数にする: " + val},
		})
		if cfg.Options != nil {
			t.Errorf("%s: Options = %+v", tt.src, cfg.Options)
		}
	}
	for _, src := range []string{`{ "lineNumber": { "minWidth": 1 } }`, `{ "lineNumber": { "minWidth": 20 } }`} {
		if _, errs := Parse("config.jsonc", []byte(src)); len(errs) != 0 {
			t.Errorf("%s: errs = %v", src, errs)
		}
	}
}

// 本体の表の yank.highlightMs は、"yank": { "highlightMs": … } で書ける。
// 範囲（0 から 10000）の外の値は、位置付きの誤りとして知らせて既定値を使う。
//
// req: FR-140
func TestParseYankHighlightOption(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{ "yank": { "highlightMs": 300 } }`))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if want := map[string]option.Value{"yank.highlightMs": option.IntValue(300)}; !reflect.DeepEqual(cfg.Options, want) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, want)
	}
	for _, val := range []string{"-1", "10001"} {
		cfg, errs := Parse("config.jsonc", []byte(`{ "yank": { "highlightMs": `+val+` } }`))
		wantErrors(t, errs, []struct {
			line, col int
			msg       string
		}{
			{1, 28, "yank.highlightMs は 0 から 10000 の整数にする: " + val},
		})
		if cfg.Options != nil {
			t.Errorf("%s: Options = %+v", val, cfg.Options)
		}
	}
}

// コマンドラインの補完の 3 項目は、"commandLine" と "completionMenu" の下に書ける（オーナーの nvim の値）。
// 補い方の値が語の列として読めなければ、位置付きの誤りとして知らせて既定値を使う。
//
// req: FR-196, FR-197
func TestParseCompletionOptions(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{ "commandLine": { "completeMode": "longest:full,full", "completeIgnoreCase": true }, "completionMenu": { "height": 12 } }`))
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	want := map[string]option.Value{
		option.CommandLineCompleteMode:       option.StringValue("longest:full,full"),
		option.CommandLineCompleteIgnoreCase: option.BoolValue(true),
		option.CompletionMenuHeight:          option.IntValue(12),
	}
	if !reflect.DeepEqual(cfg.Options, want) {
		t.Errorf("Options = %+v, want %+v", cfg.Options, want)
	}
	cfg, errs = Parse("config.jsonc", []byte(`{ "commandLine": { "completeMode": "list:full" } }`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{1, 36, "commandLine.completeMode は full、longest、noselect の語をコロンとコンマで区切って書く（部分は 4 個まで）: list:full"},
	})
	if cfg.Options != nil {
		t.Errorf("Options = %+v", cfg.Options)
	}
}

// 本体の表の filetype はバッファごとの項目なので、設定ファイルに書けば誤りとして知らせる。
//
// req: FR-131
func TestParseFileTypeOption(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{ "filetype": "go" }`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{1, 3, "filetype はバッファを開くときに決まる設定なので、設定ファイルには書けない"},
	})
	if cfg.Options != nil {
		t.Errorf("Options = %+v", cfg.Options)
	}
}

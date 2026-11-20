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
	{Name: "buf.kind", Type: option.String, Scope: option.ScopeBuffer, Default: option.StringValue("")},
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
		{3, 12, "buf.kind はバッファごとの設定なので、設定ファイルには書けない"},
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
		if d.Scope == option.ScopeEditor {
			if len(errs) != 0 || cfg.Options[d.Name] != d.Default {
				t.Errorf("%s: Options = %+v, errs = %v（既存の項目と最上位の段が重なっていないか）", d.Name, cfg.Options, errs)
			}
			continue
		}
		if len(errs) != 1 || !strings.Contains(errs[0].Error(), d.Name+" はバッファごとの設定") {
			t.Errorf("%s: errs = %v（既存の項目と最上位の段が重なっていないか）", d.Name, errs)
		}
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

// 本体の表の filetype はバッファごとの項目なので、設定ファイルに書けば誤りとして知らせる。
//
// req: FR-131
func TestParseFileTypeOption(t *testing.T) {
	cfg, errs := Parse("config.jsonc", []byte(`{ "filetype": "go" }`))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{1, 3, "filetype はバッファごとの設定なので、設定ファイルには書けない"},
	})
	if cfg.Options != nil {
		t.Errorf("Options = %+v", cfg.Options)
	}
}

package config

import (
	"errors"
	"reflect"
	"testing"
)

// commandAliases は、別名を書いた順に読み、min を省けば名前の長さにする。形の誤りのある別名は位置付きの誤りにして使わない。
//
// req: FR-150
func TestParseCommandAliases(t *testing.T) {
	src := `{
  "commandAliases": {
    "tabedit": { "command": "edit", "min": 4 },
    "tabnew": { "command": "tab.new" },
    "ff": { "command": "finder.files", "min": 1 },
    "nocmd": { "min": 2 },
    "empty": { "command": "" },
    "typo": { "command": "edit", "mni": 2 },
    "zero": { "command": "edit", "min": 0 },
    "str": "edit",
    "num": { "command": 1 },
  },
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{6, 14, "commandAliases.nocmd: 付け替える先のコマンドの名前（command）が無い"},
		{7, 27, "commandAliases.empty.command: 付け替える先のコマンドの名前が空"},
		{8, 34, "commandAliases.typo: 未知の項目 mni"},
		{9, 41, "commandAliases.zero.min は 1 以上"},
		{10, 12, "commandAliases.str"},
		{11, 25, "commandAliases.num.command"},
	})
	want := []CommandAlias{{Name: "tabedit", Min: 4, Command: "edit"}, {Name: "tabnew", Min: 6, Command: "tab.new"}, {Name: "ff", Min: 1, Command: "finder.files"}}
	if !reflect.DeepEqual(cfg.CommandAliases, want) {
		t.Errorf("CommandAliases = %+v, want %+v", cfg.CommandAliases, want)
	}
	if _, errs := Parse("config.jsonc", []byte(`{"commandAliases": []}`)); len(errs) != 1 {
		t.Errorf("配列の commandAliases: errs = %v", errs)
	}
}

// CheckCommandAliases は、書いた順に別名を足す関数へ渡し、足せなかった別名を位置付きの誤りにして除く。
//
// req: FR-150
func TestCheckCommandAliases(t *testing.T) {
	src := `{
  "commandAliases": {
    "tabedit": { "command": "edit", "min": 4 },
    "bad": { "command": "nosuch" },
    "tabnew": { "command": "tab.new" },
  },
}`
	cfg, errs := Parse("config.jsonc", []byte(src))
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	var order []string
	errs = cfg.CheckCommandAliases(func(a CommandAlias) error {
		order = append(order, a.Name)
		if a.Command == "nosuch" {
			return errors.New("知らない付け替え先")
		}
		return nil
	})
	wantErrors(t, errs, []struct {
		line, col int
		msg       string
	}{
		{4, 5, "commandAliases.bad: 別名を使えない: 知らない付け替え先"},
	})
	if want := []string{"tabedit", "bad", "tabnew"}; !reflect.DeepEqual(order, want) {
		t.Errorf("足した順 = %v, want %v", order, want)
	}
	want := []CommandAlias{{Name: "tabedit", Min: 4, Command: "edit"}, {Name: "tabnew", Min: 6, Command: "tab.new"}}
	if !reflect.DeepEqual(cfg.CommandAliases, want) {
		t.Errorf("CommandAliases = %+v, want %+v", cfg.CommandAliases, want)
	}
	// Parse を通さずに作った Config でも、位置の無い誤りで知らせる。
	c := Config{CommandAliases: []CommandAlias{{Name: "x", Min: 1, Command: "y"}}}
	if errs := c.CheckCommandAliases(func(CommandAlias) error { return errors.New("no") }); len(errs) != 1 || errs[0].Error() != ": commandAliases.x: 別名を使えない: no" {
		t.Errorf("位置の無い Config: errs = %v", errs)
	}
}

package ex

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// :option.toggle は真偽の項目を反転する。真偽でない項目と、名前の後に続く文字は E488、知らない名前は E518、名前が無ければ E471 にする。
//
// req: FR-155
func TestOptionToggle(t *testing.T) {
	withTestOptions(t)
	tests := []struct {
		name     string
		cmdline  string
		init     map[string]option.Value
		wantSets []string
		wantCode string
		wantPos  int
	}{
		{name: "偽から真", cmdline: "option.toggle test.flag", wantSets: []string{"test.flag=true"}},
		{name: "真から偽", cmdline: "option.toggle test.flag", init: map[string]option.Value{"test.flag": option.BoolValue(true)}, wantSets: []string{"test.flag=false"}},
		{name: "続けて反転", cmdline: "option.toggle test.flag|option.toggle test.flag", wantSets: []string{"test.flag=true", "test.flag=false"}},
		{name: "本体の項目", cmdline: "option.toggle lineNumber.show", wantSets: []string{"lineNumber.show=true"}},
		{name: "末尾の空白", cmdline: "option.toggle  test.flag \t", wantSets: []string{"test.flag=true"}},
		{name: "文字列の項目", cmdline: "option.toggle filetype", wantCode: "E488", wantPos: 14},
		{name: "整数の項目", cmdline: "option.toggle test.count", wantCode: "E488", wantPos: 14},
		{name: "名前の後の文字", cmdline: "option.toggle test.flag x", wantCode: "E488", wantPos: 24},
		{name: "知らない名前", cmdline: "option.toggle number", wantCode: "E518", wantPos: 14},
		{name: "名前が無い", cmdline: "option.toggle", wantCode: "E471", wantPos: -1},
		{name: "省略できない", cmdline: "option.tog test.flag", wantCode: "E492", wantPos: -1},
		{name: "範囲", cmdline: "2option.toggle test.flag", wantCode: "E481", wantPos: 0},
		{name: "!", cmdline: "option.toggle! test.flag", wantCode: "E477", wantPos: 13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &fakeOptions{vals: maps.Clone(tt.init)}
			_, err := Execute(buffer.New("a\n"), &State{Options: opts}, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if e := (*Error)(nil); tt.wantCode != "" && tt.wantPos >= 0 && errors.As(err, &e) && e.Pos != tt.wantPos {
				t.Errorf("pos = %d, want %d (err = %v)", e.Pos, tt.wantPos, err)
			}
			if !slices.Equal(opts.sets, tt.wantSets) {
				t.Errorf("sets = %q, want %q", opts.sets, tt.wantSets)
			}
		})
	}
	if _, err := Execute(buffer.New("a\n"), &State{}, 0, "option.toggle test.flag"); errCode(t, err) != "E319" {
		t.Errorf("読み書きの口が無い: err = %v, want E319", err)
	}
}

// :set 名前! は、正規のコマンド :option.toggle の呼び出しに変換する。真偽でない項目の誤りは :option.toggle が出す。
//
// req: FR-155
func TestSetBangCallsToggle(t *testing.T) {
	withTestOptions(t)
	for arg, want := range map[string]string{
		"flag!":     "option.toggle test.flag",
		"number!":   "option.toggle lineNumber.show",
		"hlsearch!": "option.toggle search.highlight",
		"filetype!": "option.toggle filetype",
	} {
		if got, err := setCall(arg, 4); err != nil || got != want {
			t.Errorf("setCall(%q) = %q, %v, want %q", arg, got, err, want)
		}
	}
	_, err := Execute(buffer.New("a\n"), &State{Options: &fakeOptions{}}, 0, "set filetype!")
	if e := (*Error)(nil); !errors.As(err, &e) || e.Code != "E488" || e.Pos != 4 || !strings.Contains(e.Msg, "真偽の設定ではないので反転できません") {
		t.Errorf(":set filetype!: err = %#v", err)
	}
}

// keymap の右辺の Ex コマンドに書けるのは、本体の Ex コマンドの正規の名前と、拡張機能のコマンドの ID だけである。
// 正規の名前の無いコマンド、省略形、別名（本体の別名と設定ファイルの別名）、本体の名前に隠れる拡張機能のコマンド、知らない名前は誤りにする。
//
// req: FR-154
func TestKeymapCommandLine(t *testing.T) {
	aliases := NewAliases()
	if err := aliases.Add(RenameAlias{Name: "tabnew", Min: 6, Target: "tab.new"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := aliases.Add(RenameAlias{Name: "ff", Min: 2, Target: "finder.files"}, []string{"finder.files"}); err != nil {
		t.Fatal(err)
	}
	ext := []string{"finder.files", "acp.prompt", "hover", "s"}
	tests := []struct {
		name, args string
		want       string
		wantErr    string
	}{
		{name: "search.clearHighlight", want: "search.clearHighlight"},
		{name: "option.toggle", args: "lineNumber.show", want: "option.toggle lineNumber.show"},
		{name: "option.set", args: "filetype a|b", want: "option.set filetype a|b"},
		{name: "option.toggle", args: "lineNumber.show|w", wantErr: "args に、コマンドの区切りになる | を書けない"},
		{name: "search.clearHighlight", args: "|write", wantErr: "args に、コマンドの区切りになる | を書けない"},
		{name: "tab.new", args: `a\|b`, want: `tab.new a\|b`},
		{name: "acp.prompt", args: "a | b", want: "acp.prompt a | b"},
		{name: "tab.new", want: "tab.new"},
		{name: "finder.files", want: "finder.files"},
		{name: "acp.prompt", args: "説明して", want: "acp.prompt 説明して"},
		{name: "hover", want: "hover"},
		{name: "", wantErr: "空"},
		{name: "option.set x", wantErr: "空白"},
		{name: "tab.new!", wantErr: "空白"},
		{name: "write", wantErr: "正規の名前の無い Ex コマンド（:write）"},
		{name: "w", wantErr: "正規の名前の無い Ex コマンド（:write）"},
		{name: "s", wantErr: "正規の名前の無い Ex コマンド（:substitute）"},
		{name: "option.se", wantErr: "正規の名前でも"},
		{name: "nohlsearch", wantErr: "別名なので書けない（正規の名前 search.clearHighlight を書く）"},
		{name: "noh", wantErr: "別名なので書けない"},
		{name: "set", wantErr: "別名なので書けない（正規の名前のコマンドを書く）"},
		{name: "tabnew", wantErr: "別名なので書けない（正規の名前 tab.new を書く）"},
		{name: "ff", wantErr: "別名なので書けない（正規の名前 finder.files を書く）"},
		{name: "help", wantErr: "正規の名前でも"},
		{name: "nosuch.cmd", wantErr: "正規の名前でも"},
	}
	for _, tt := range tests {
		got, err := KeymapCommandLine(aliases, tt.name, tt.args, ext)
		switch {
		case tt.wantErr != "":
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("%q: err = %v, want %q を含む", tt.name, err, tt.wantErr)
			}
		case err != nil || got != tt.want:
			t.Errorf("%q %q = %q, %v, want %q", tt.name, tt.args, got, err, tt.want)
		}
	}
	// 別名の表が nil なら本体の別名だけを引く。
	if _, err := KeymapCommandLine(nil, "tabnew", "", ext); err == nil || !strings.Contains(err.Error(), "正規の名前でも") {
		t.Errorf("nil の表の tabnew: err = %v", err)
	}
}

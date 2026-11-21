package ex

import (
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// Complete は、行末の語の位置と種類を、Execute と同じ表で読んで決める。範囲は読み飛ばし、"|" で続けたコマンドは最後のコマンドを読む。
// 補う引数を持たないコマンドと知らないコマンドは、補うものが無い（<Tab> は文字として入る）。
// 拡張機能のコマンドの口から呼ぶコマンドは、別名を付け替えた後の ID で返す。
//
// req: FR-195
func TestCompleteContext(t *testing.T) {
	aliases := NewAliases()
	if err := aliases.Add(RenameAlias{Name: "tabedit", Min: 4, Target: tabNewName}, nil); err != nil {
		t.Fatal(err)
	}
	if err := aliases.Add(RenameAlias{Name: "ff", Min: 2, Target: "finder.files"}, []string{"finder.files"}); err != nil {
		t.Fatal(err)
	}
	st := &State{
		Aliases: aliases,
		ExtCommand: func(id string) (string, bool) {
			switch id {
			case "finder.files", "acp.prompt":
				return strings.Split(id, ".")[0], true
			case "help", "h":
				return " help", true
			}
			return "", false
		},
	}
	for _, tt := range []struct {
		line    string
		kind    CompletionKind
		start   int
		command string
	}{
		{"", CompleteCommand, 0, ""},
		{"e", CompleteCommand, 0, ""},
		{"  :e", CompleteCommand, 3, ""},
		{"%s", CompleteCommand, 1, ""},
		{"'<,'>s", CompleteCommand, 5, ""},
		{"3,", CompleteCommand, 2, ""},
		{"finder.f", CompleteCommand, 0, ""},
		{"3|d", CompleteCommand, 2, ""},
		{"e ", CompleteFile, 2, ""},
		{"e a", CompleteFile, 2, ""},
		{"e! ~/a", CompleteFile, 3, ""},
		{"edit dir/sp\\ a", CompleteFile, 5, ""},
		{"w a b", CompleteFile, 4, ""},
		{"wq a", CompleteFile, 3, ""},
		{"x a", CompleteFile, 2, ""},
		{"tab.new a", CompleteFile, 8, ""},
		{"tabe a", CompleteFile, 5, ""},
		{"diffs a", CompleteFile, 6, ""},
		{"w | e a", CompleteFile, 6, ""},
		{"w a\\|b", CompleteFile, 2, ""},
		{"b a", CompleteBuffer, 2, ""},
		{"bd foo bar", CompleteBuffer, 3, ""},
		{"he ", CompleteNothing, 0, ""},
		{"help fo", CompleteExtCommand, 5, "help"},
		{"h", CompleteCommand, 0, ""},
		{"h  x", CompleteExtCommand, 3, "h"},
		{"acp.prompt a|b", CompleteExtCommand, 11, "acp.prompt"},
		{"ff x", CompleteExtCommand, 3, "finder.files"},
		{"w !ls", CompleteNothing, 0, ""},
		{"w >>a", CompleteNothing, 0, ""},
		{"!ls", CompleteNothing, 0, ""},
		{"q ", CompleteNothing, 0, ""},
		{"s/a/b", CompleteNothing, 0, ""},
		{"g/a/e x", CompleteNothing, 0, ""},
		{"nosuch a", CompleteNothing, 0, ""},
		{"e3", CompleteCommand, 0, ""},
		{"e3 ", CompleteNothing, 0, ""},
		{"q! ", CompleteNothing, 0, ""},
		{"d! ", CompleteNothing, 0, ""},
		{`" e a`, CompleteNothing, 0, ""},
	} {
		c := Complete(st, tt.line)
		if c.Kind != tt.kind || c.Start != tt.start || c.Command != tt.command {
			t.Errorf("Complete(%q) = %+v, want kind %d start %d command %q", tt.line, c, tt.kind, tt.start, tt.command)
		}
	}
}

// 設定の名前は、:option.get、:option.set、:option.toggle（真偽の項目だけ）では正規の名前を、:set では Vim の名前を、名前の順に補う。
// :set の名前は、長い名前か短い名前が語で始まる項目の長い名前である。"no" の後には真偽の項目の名前を補う。
// 値は、真偽なら true と false、語の列なら最後の区切りの後の語、それ以外は今の値（:set では "\" を付けた形）である。
//
// req: FR-195
func TestCompleteOptions(t *testing.T) {
	opts := &fakeOptions{vals: map[string]option.Value{
		option.CommentString:           option.StringValue("// %s"),
		option.CommandLineCompleteMode: option.StringValue("longest:full,full"),
	}}
	st := &State{Options: opts}
	for _, tt := range []struct {
		line  string
		start int
		want  []string
	}{
		{"option.get lineNumber.", 11, []string{option.LineNumberMinWidth, option.LineNumberShow}},
		{"option.toggle lineNumber.", 14, []string{option.LineNumberShow}},
		{"option.set commandLine.", 11, []string{option.CommandLineCompleteIgnoreCase, option.CommandLineCompleteMode}},
		{"option.set lineNumber.show ", 27, []string{"true", "false"}},
		{"option.set lineNumber.show f", 27, []string{"false"}},
		{"option.set tab.width ", 21, []string{"8"}},
		{"option.set tab.width 4", 21, nil},
		{"option.set comment.string ", 26, []string{"// %s"}},
		{"option.set commandLine.completeMode ", 36, []string{"longest:full,full", "full", "longest", "noselect"}},
		{"option.set commandLine.completeMode longest:", 44, []string{"full", "longest", "noselect"}},
		{"option.set commandLine.completeMode full,l", 41, []string{"longest"}},
		{"set nu", 4, []string{"number", "numberwidth"}},
		{"set ts", 4, []string{"tabstop"}},
		{"se s", 3, []string{"scrolloff", "shiftwidth", "sidescrolloff"}},
		{"set nu ts", 7, []string{"tabstop"}},
		{"set no", 6, []string{"autoindent", "expandtab", "hlsearch", "list", "number", "undofile", "wildignorecase", "wrap"}},
		{"set nonu", 6, []string{"number"}},
		{"set wild", 4, []string{"wildignorecase", "wildmode"}},
		{"set ts=", 7, []string{"8"}},
		{"set cms=", 8, []string{`//\ %s`}},
		{"set wildmode=", 13, []string{"longest:full,full", "full", "longest", "noselect"}},
		{"set wildmode=longest:f", 21, []string{"full"}},
		{"set w|set nu", 10, []string{"number", "numberwidth"}},
	} {
		c := Complete(st, tt.line)
		if c.Kind != CompleteList || c.Start != tt.start || !slices.Equal(c.Candidates, tt.want) {
			t.Errorf("Complete(%q) = %+v, want start %d candidates %q", tt.line, c, tt.start, tt.want)
		}
	}
	for _, line := range []string{"set nu!", "set nu?", "set number=", "set nosuch=", "option.set nosuch ", "option.get a b", "set ts&"} {
		if c := Complete(st, line); c.Kind != CompleteNothing {
			t.Errorf("Complete(%q) = %+v, want 補うものが無い", line, c)
		}
	}
}

// ファイルとバッファの名前は、空白と "|" の前に "\" を付けて書き、fileArg と splitBar と同じく戻す。
//
// req: FR-195
func TestEscapeFileArg(t *testing.T) {
	for _, tt := range []struct{ name, arg string }{
		{"a.txt", "a.txt"},
		{"sp ace.txt", `sp\ ace.txt`},
		{"a|b", `a\|b`},
		{"日本 語/x", `日本\ 語/x`},
	} {
		if got := EscapeFileArg(tt.name); got != tt.arg {
			t.Errorf("EscapeFileArg(%q) = %q, want %q", tt.name, got, tt.arg)
		}
		if got := UnescapeFileArg(tt.arg); got != tt.name {
			t.Errorf("UnescapeFileArg(%q) = %q, want %q", tt.arg, got, tt.name)
		}
		// :e で開くファイルの名前も同じになる。
		res, err := Execute(buffer.New("x\n"), nil, 0, "e "+tt.arg)
		if err != nil || len(res.Actions) != 1 || res.Actions[0].File != tt.name {
			t.Errorf(":e %s = %+v, %v", tt.arg, res.Actions, err)
		}
	}
}

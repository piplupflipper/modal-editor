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

// fakeOptions は、設定の値を読み書きする口の偽物である。Set で設定した "<名前>=<値>" を順に記録する。
type fakeOptions struct {
	vals map[string]option.Value
	sets []string
}

func (f *fakeOptions) Get(d option.Def) option.Value {
	if v, ok := f.vals[d.Name]; ok {
		return v
	}
	return d.Default
}

func (f *fakeOptions) Set(d option.Def, v option.Value) error {
	if v.Type() != d.Type {
		return errors.New("type mismatch")
	}
	if f.vals == nil {
		f.vals = map[string]option.Value{}
	}
	f.vals[d.Name] = v
	f.sets = append(f.sets, d.Name+"="+v.String())
	return nil
}

// withTestOptions は、本体の表に無い型（真偽と整数）の項目を、設定の表と :set の Vim の名前の対応に足す。
// 足した項目は test.flag（真偽。:set では flag）と test.count（整数。:set では count）である。
func withTestOptions(t *testing.T) {
	t.Helper()
	extra := map[string]option.Def{
		"test.flag":  {Name: "test.flag", Type: option.Bool, Scope: option.ScopeEditor, Default: option.BoolValue(false)},
		"test.count": {Name: "test.count", Type: option.Int, Scope: option.ScopeBuffer, Default: option.IntValue(8)},
	}
	prevLookup, prevNames := optionLookup, vimOptionNames
	optionLookup = func(name string) (option.Def, bool) {
		if d, ok := extra[name]; ok {
			return d, true
		}
		return option.Lookup(name)
	}
	vimOptionNames = maps.Clone(vimOptionNames)
	vimOptionNames["flag"], vimOptionNames["count"] = "test.flag", "test.count"
	t.Cleanup(func() { optionLookup, vimOptionNames = prevLookup, prevNames })
}

// :option.set は名前を表で引き、値を型に合わせて読んで設定する。:option.get は "<名前>=<値>" を通知に出す。
// 別名の :set は、6 つの書き方を :option.set と :option.get の呼び出しに変換する。
//
// req: FR-131, FR-132, FR-133
func TestOptionCommands(t *testing.T) {
	withTestOptions(t)
	tests := []struct {
		name       string
		cmdline    string
		init       map[string]option.Value
		wantSets   []string
		wantNotice string
		wantCode   string
		wantPos    int // wantCode があるときの誤りの位置（-1 なら確かめない）
	}{
		// :option.set
		{name: "文字列", cmdline: "option.set filetype go", wantSets: []string{"filetype=go"}},
		{name: "文字列は空白の後の行末まで（末尾の空白は除く）", cmdline: "option.set  filetype   a|b c \t ", wantSets: []string{"filetype=a|b c"}},
		{name: "真偽の末尾の空白", cmdline: "option.set test.flag true ", wantSets: []string{"test.flag=true"}},
		{name: "整数の末尾の空白", cmdline: "option.set test.count 5\t", wantSets: []string{"test.count=5"}},
		{name: "空白だけの値", cmdline: "option.set filetype   ", wantSets: []string{"filetype="}},
		{name: "空の文字列", cmdline: "option.set filetype", wantSets: []string{"filetype="}},
		{name: "値は | を含めて読む", cmdline: "option.set test.flag true|option.set test.flag false", wantCode: "E474", wantPos: 21},
		{name: "真偽の true", cmdline: "option.set test.flag true", wantSets: []string{"test.flag=true"}},
		{name: "真偽の false", cmdline: "option.set test.flag false", wantSets: []string{"test.flag=false"}},
		{name: "真偽でない値", cmdline: "option.set test.flag yes", wantCode: "E474", wantPos: 21},
		{name: "真偽の 1", cmdline: "option.set test.flag 1", wantCode: "E474", wantPos: 21},
		{name: "整数", cmdline: "option.set test.count 12", wantSets: []string{"test.count=12"}},
		{name: "負の整数", cmdline: "option.set test.count -3", wantSets: []string{"test.count=-3"}},
		{name: "整数でない値", cmdline: "option.set test.count abc", wantCode: "E521", wantPos: 22},
		{name: "整数の空", cmdline: "option.set test.count", wantCode: "E521", wantPos: -1},
		{name: "整数の +", cmdline: "option.set test.count +5", wantCode: "E521", wantPos: 22},
		{name: "整数の 16 進", cmdline: "option.set test.count 0x10", wantCode: "E521", wantPos: 22},
		{name: "知らない名前", cmdline: "option.set number true", wantCode: "E518", wantPos: 11},
		{name: "名前が無い", cmdline: "option.set", wantCode: "E471", wantPos: -1},
		{name: "省略できない", cmdline: "option.s filetype go", wantCode: "E492", wantPos: -1},
		{name: "点の前だけ", cmdline: "option filetype go", wantCode: "E492", wantPos: -1},
		{name: "範囲", cmdline: "2option.set filetype go", wantCode: "E481", wantPos: 0},
		{name: "!", cmdline: "option.set! filetype go", wantCode: "E477", wantPos: 10},
		{name: ":g の中", cmdline: "g/a/option.set filetype go", wantCode: "E319", wantPos: -1},
		// :option.get
		{name: "読む", cmdline: "option.get filetype", init: map[string]option.Value{"filetype": option.StringValue("go")}, wantNotice: "filetype=go"},
		{name: "既定値を読む", cmdline: "option.get test.count", wantNotice: "test.count=8"},
		{name: "続けて読む", cmdline: "option.get filetype|option.get test.flag", wantNotice: "filetype= test.flag=false"},
		{name: "設定した後に読む", cmdline: "option.set test.count 3", init: map[string]option.Value{"test.count": option.IntValue(1)}, wantSets: []string{"test.count=3"}},
		{name: "読んだ後に設定する", cmdline: "option.get test.count|option.set test.count 3", wantSets: []string{"test.count=3"}, wantNotice: "test.count=8"},
		{name: "読む名前が無い", cmdline: "option.get", wantCode: "E471", wantPos: -1},
		{name: "名前の後の文字", cmdline: "option.get filetype x", wantCode: "E488", wantPos: 20},
		{name: "読む名前を知らない", cmdline: "option.get nosuch", wantCode: "E518", wantPos: 11},
		// :set の 6 つの書き方
		{name: ":set 真偽の名前", cmdline: "set flag", wantSets: []string{"test.flag=true"}},
		{name: ":set no 名前", cmdline: "set noflag", init: map[string]option.Value{"test.flag": option.BoolValue(true)}, wantSets: []string{"test.flag=false"}},
		{name: ":set 名前! で反転", cmdline: "set flag!", wantSets: []string{"test.flag=true"}},
		{name: ":set 名前! で反転（真から）", cmdline: "set flag!", init: map[string]option.Value{"test.flag": option.BoolValue(true)}, wantSets: []string{"test.flag=false"}},
		{name: ":set 名前=値", cmdline: "set filetype=go", wantSets: []string{"filetype=go"}},
		{name: ":set 名前=値（整数）", cmdline: "set count=5", wantSets: []string{"test.count=5"}},
		{name: ":set 名前=（空）", cmdline: "set filetype=", init: map[string]option.Value{"filetype": option.StringValue("go")}, wantSets: []string{"filetype="}},
		{name: ":set 名前?", cmdline: "set filetype?", init: map[string]option.Value{"filetype": option.StringValue("go")}, wantNotice: "filetype=go"},
		{name: ":set 真偽の名前?", cmdline: "set flag?", wantNotice: "test.flag=false"},
		{name: ":set 名前 名前 …", cmdline: "set filetype=go  flag\tcount=3", wantSets: []string{"filetype=go", "test.flag=true", "test.count=3"}},
		{name: ":set 反転を続ける", cmdline: "set flag! flag! flag!", wantSets: []string{"test.flag=true", "test.flag=false", "test.flag=true"}},
		{name: ":set 設定してから読む", cmdline: "set filetype=c filetype?", wantSets: []string{"filetype=c"}, wantNotice: "filetype=c"},
		{name: ":se", cmdline: "se filetype=go", wantSets: []string{"filetype=go"}},
		{name: ":set の後の |", cmdline: "set filetype=a|option.get filetype", wantSets: []string{"filetype=a"}, wantNotice: "filetype=a"},
		{name: ":set の \\|", cmdline: `set filetype=a\|b`, wantSets: []string{"filetype=a|b"}},
		// 真偽でない項目の :set
		{name: ":set 文字列の名前は値を出す", cmdline: "set filetype", init: map[string]option.Value{"filetype": option.StringValue("go")}, wantNotice: "filetype=go"},
		{name: ":set no 文字列の名前", cmdline: "set nofiletype", wantCode: "E474", wantPos: 4},
		{name: ":set 文字列の名前!", cmdline: "set filetype!", wantCode: "E488", wantPos: 4},
		{name: ":set 真偽の名前=値", cmdline: "set flag=1", wantCode: "E474", wantPos: 4},
		{name: ":set 整数でない値", cmdline: "set count=x", wantCode: "E521", wantPos: 4},
		{name: ":set 整数の名前=（空）", cmdline: "set count=", wantCode: "E521", wantPos: 4},
		// :set で受け付けない書き方
		{name: ":set 知らない名前", cmdline: "set nosuch", wantCode: "E518", wantPos: 4},
		{name: ":set 正規の名前", cmdline: "set test.flag", wantCode: "E518", wantPos: 4},
		{name: ":set inv", cmdline: "set invflag", wantCode: "E518", wantPos: 4},
		{name: ":set +=", cmdline: "set count+=1", wantCode: "E518", wantPos: 4},
		{name: ":set -=", cmdline: "set count-=1", wantCode: "E518", wantPos: 4},
		{name: ":set ^=", cmdline: "set count^=1", wantCode: "E518", wantPos: 4},
		{name: ":set &", cmdline: "set flag&", wantCode: "E518", wantPos: 4},
		{name: ":set 短い名前", cmdline: "set ft=go", wantCode: "E518", wantPos: 4},
		{name: ":set 引数が無い", cmdline: "set", wantCode: "E471", wantPos: -1},
		{name: ":set 空白だけ", cmdline: "set  ", wantCode: "E471", wantPos: -1},
		{name: ":setlocal", cmdline: "setlocal filetype=go", wantCode: "E492", wantPos: -1},
		{name: ":s は :substitute", cmdline: "s filetype=go", wantCode: "E146", wantPos: -1},
		{name: ":set の範囲", cmdline: "3set flag", wantCode: "E481", wantPos: 0},
		{name: ":set!", cmdline: "set! flag", wantCode: "E477", wantPos: 3},
		{name: ":set の途中の誤りで止まる", cmdline: "set filetype=go nosuch flag", wantSets: []string{"filetype=go"}, wantCode: "E518", wantPos: 16},
		{name: ":set の変換の誤りの位置", cmdline: "set flag count=y", wantSets: []string{"test.flag=true"}, wantCode: "E521", wantPos: 9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &fakeOptions{vals: maps.Clone(tt.init)}
			st := &State{Options: opts}
			res, err := Execute(buffer.New("a\nb\n"), st, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if e := (*Error)(nil); tt.wantCode != "" && tt.wantPos >= 0 && errors.As(err, &e) && e.Pos != tt.wantPos {
				t.Errorf("pos = %d, want %d (err = %v)", e.Pos, tt.wantPos, err)
			}
			if tt.wantCode == "" || tt.wantSets != nil {
				if !slices.Equal(opts.sets, tt.wantSets) {
					t.Errorf("sets = %q, want %q", opts.sets, tt.wantSets)
				}
			}
			if tt.wantCode == "" && res.Notice.Text != tt.wantNotice {
				t.Errorf("notice = %q, want %q", res.Notice.Text, tt.wantNotice)
			}
		})
	}
}

// 行番号の 2 つの項目は、:set の Vim の名前 number と numberwidth で読み書きでき、範囲の外の値は Vim 9.1 と同じ番号の誤りにする
// （2026-11-20 に vim -Nu NONE で、:set numberwidth=0 と =-1 が E487、=21 が E474、=abc が E521 になることを確かめた）。
//
// req: FR-135, FR-136
func TestLineNumberOptions(t *testing.T) {
	tests := []struct {
		name       string
		cmdline    string
		init       map[string]option.Value
		wantSets   []string
		wantNotice string
		wantCode   string
		wantPos    int
	}{
		{name: ":set number", cmdline: "set number", wantSets: []string{"lineNumber.show=true"}},
		{name: ":set nonumber", cmdline: "set nonumber", init: map[string]option.Value{"lineNumber.show": option.BoolValue(true)}, wantSets: []string{"lineNumber.show=false"}},
		{name: ":set number! で出す", cmdline: "set number!", wantSets: []string{"lineNumber.show=true"}},
		{name: ":set number! で消す", cmdline: "set number!", init: map[string]option.Value{"lineNumber.show": option.BoolValue(true)}, wantSets: []string{"lineNumber.show=false"}},
		{name: ":set number?", cmdline: "set number?", wantNotice: "lineNumber.show=false"},
		{name: ":set numberwidth=5", cmdline: "set numberwidth=5", wantSets: []string{"lineNumber.minWidth=5"}},
		{name: ":set numberwidth は値を出す", cmdline: "set numberwidth", wantNotice: "lineNumber.minWidth=4"},
		{name: ":set numberwidth=1（下限）", cmdline: "set numberwidth=1", wantSets: []string{"lineNumber.minWidth=1"}},
		{name: ":set numberwidth=20（上限）", cmdline: "set numberwidth=20", wantSets: []string{"lineNumber.minWidth=20"}},
		{name: ":set numberwidth=0", cmdline: "set numberwidth=0", wantCode: "E487", wantPos: 4},
		{name: ":set numberwidth=-1", cmdline: "set numberwidth=-1", wantCode: "E487", wantPos: 4},
		{name: ":set numberwidth=21", cmdline: "set numberwidth=21", wantCode: "E474", wantPos: 4},
		{name: ":set numberwidth=abc", cmdline: "set numberwidth=abc", wantCode: "E521", wantPos: 4},
		{name: ":set number=1", cmdline: "set number=1", wantCode: "E474", wantPos: 4},
		{name: ":set nonumberwidth", cmdline: "set nonumberwidth", wantCode: "E474", wantPos: 4},
		{name: ":set numberwidth!", cmdline: "set numberwidth!", wantCode: "E488", wantPos: 4},
		{name: ":set nu（短い名前）", cmdline: "set nu", wantCode: "E518", wantPos: 4},
		{name: ":option.set の範囲の下", cmdline: "option.set lineNumber.minWidth 0", wantCode: "E487", wantPos: 31},
		{name: ":option.set の範囲の上", cmdline: "option.set lineNumber.minWidth 21", wantCode: "E474", wantPos: 31},
		{name: ":option.set の範囲の中", cmdline: "option.set lineNumber.minWidth 5", wantSets: []string{"lineNumber.minWidth=5"}},
		{name: ":option.set lineNumber.show", cmdline: "option.set lineNumber.show true", wantSets: []string{"lineNumber.show=true"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &fakeOptions{vals: maps.Clone(tt.init)}
			st := &State{Options: opts}
			res, err := Execute(buffer.New("a\nb\n"), st, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if e := (*Error)(nil); tt.wantCode != "" && errors.As(err, &e) && e.Pos != tt.wantPos {
				t.Errorf("pos = %d, want %d (err = %v)", e.Pos, tt.wantPos, err)
			}
			if !slices.Equal(opts.sets, tt.wantSets) {
				t.Errorf("sets = %q, want %q", opts.sets, tt.wantSets)
			}
			if tt.wantCode == "" && res.Notice.Text != tt.wantNotice {
				t.Errorf("notice = %q, want %q", res.Notice.Text, tt.wantNotice)
			}
		})
	}
}

// 設定の値を読み書きする口が無ければ、:option.set、:option.get、:set は E319 にする。
//
// req: FR-132
func TestOptionCommandsWithoutOptions(t *testing.T) {
	for _, cmdline := range []string{"option.set filetype go", "option.get filetype", "set filetype=go", "set filetype?"} {
		if _, err := Execute(buffer.New("a\n"), nil, 0, cmdline); errCode(t, err) != "E319" {
			t.Errorf("%s: err = %v, want E319", cmdline, err)
		}
	}
}

// 本体の Ex コマンドと別名の名前は、同じ ID の拡張機能のコマンドより優先する。
//
// req: FR-133
func TestOptionCommandsOverExtCommands(t *testing.T) {
	st := &State{
		Options:    &fakeOptions{},
		ExtCommand: func(id string) (string, bool) { return "x", id == "set" || id == "se" || id == "option.set" },
	}
	for _, cmdline := range []string{"set filetype=go", "se filetype=go", "option.set filetype go"} {
		res, err := Execute(buffer.New("a\n"), st, 0, cmdline)
		if err != nil || len(res.Actions) != 0 {
			t.Errorf("%s: actions = %+v, err = %v", cmdline, res.Actions, err)
		}
	}
	if got := st.Options.(*fakeOptions).sets; !slices.Equal(got, []string{"filetype=go", "filetype=go", "filetype=go"}) {
		t.Errorf("sets = %q", got)
	}
}

// 名前を付け替えるだけの別名は、範囲、"!"、引数を正規のコマンドへそのまま渡す。
//
// req: FR-133
func TestRenameAlias(t *testing.T) {
	prev := aliases
	aliases = append(slices.Clone(aliases), alias{name: "xdelete", min: 2, target: "delete"}, alias{name: "xget", min: 4, target: optionGetName})
	t.Cleanup(func() { aliases = prev })
	b := buffer.New("a\nb\nc\n")
	if _, err := Execute(b, nil, 0, "2xd|xdel"); err != nil {
		t.Fatal(err)
	}
	// :2xd が 2 行目を消してカーソルを 3 行目だった行へ移し、:xdel がその行を消す。
	if got := b.Text(); got != "a" {
		t.Errorf("text = %q, want %q", got, "a")
	}
	if _, err := Execute(b, nil, 0, "xd!"); errCode(t, err) != "E477" {
		t.Errorf(":xd!: err = %v, want E477", err)
	}
	res, err := Execute(b, &State{Options: &fakeOptions{}}, 0, "xget filetype")
	if err != nil || res.Notice.Text != "filetype=" {
		t.Errorf(":xget: notice = %q, err = %v", res.Notice.Text, err)
	}
}

// Names は、Ex コマンドの表の行の後に、別名の表の行（:set と、名前を付け替えるだけの行）を表の順に返す。
//
// req: FR-133
func TestNamesIncludeAliases(t *testing.T) {
	prev := aliases
	aliases = append(slices.Clone(aliases), alias{name: "xdelete", min: 2, target: "delete"})
	t.Cleanup(func() { aliases = prev })
	names := Names()
	if len(names) != len(commands)+len(aliases) {
		t.Fatalf("len = %d, want %d", len(names), len(commands)+len(aliases))
	}
	if got := names[len(commands):]; !slices.Equal(got, []Name{{Full: "set", Short: "se"}, {Full: "xdelete", Short: "xd"}}) {
		t.Errorf("別名の行 = %+v", got)
	}
	if names[0] != (Name{Full: "write", Short: "w"}) {
		t.Errorf("先頭 = %+v", names[0])
	}
}

// 範囲がウィンドウの番号かの判定（addrKindAt）も、点を含む正規の名前を runOne と同じく読む。
func TestAddrKindDottedName(t *testing.T) {
	prev := commands
	commands = append(slices.Clone(commands), command{name: "window.quit", min: len("window.quit"), bang: true, rng: rangeWindow, run: runQuit})
	t.Cleanup(func() { commands = prev })
	for _, cmdline := range []string{"$quit", "$window.quit", "1window.quit!"} {
		res, err := Execute(buffer.New("a\nb\nc\n"), nil, 0, cmdline)
		if err != nil || len(res.Actions) != 1 || res.Actions[0].Kind != ActionQuit {
			t.Errorf("%s: actions = %+v, err = %v", cmdline, res.Actions, err)
		}
	}
	if _, err := Execute(buffer.New("a\nb\nc\n"), nil, 0, "2window.quit"); errCode(t, err) != "E16" {
		t.Errorf(":2window.quit: err = %v, want E16", err)
	}
}

// Names は、表に入れた設定のコマンドを、省略できない名前で返す。
func TestOptionCommandNames(t *testing.T) {
	for _, n := range Names() {
		if strings.HasPrefix(n.Full, "option.") && n.Short != n.Full {
			t.Errorf("%s の省略形 = %q", n.Full, n.Short)
		}
	}
	if lookup(optionSetName) == nil || lookup(optionGetName) == nil || lookup("option.se") != nil {
		t.Errorf("lookup: option.set = %v, option.get = %v, option.se = %v", lookup(optionSetName) != nil, lookup(optionGetName) != nil, lookup("option.se") != nil)
	}
}

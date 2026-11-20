package ex

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// 設定ファイルの別名は、本体の別名と同じ表で、同じ規則（表の順、省略できる最短の長さ）で引く。
// 範囲、"!"、引数、"|" の扱いは、付け替えた先の正規のコマンドのものになる。
//
// req: FR-150
func TestAddedAliasLookup(t *testing.T) {
	al := NewAliases()
	for _, a := range []RenameAlias{{Name: "tabedit", Min: 4, Target: "edit"}, {Name: "tabnew", Min: 6, Target: tabNewName}} {
		if err := al.Add(a, nil); err != nil {
			t.Fatalf("%s: %v", a.Name, err)
		}
	}
	tests := []struct {
		cmdline  string
		want     []Action
		wantCode string
	}{
		{cmdline: "tabedit a.txt", want: []Action{{Kind: ActionEdit, File: "a.txt"}}},
		{cmdline: "tabe a.txt", want: []Action{{Kind: ActionEdit, File: "a.txt"}}},
		{cmdline: "tabed! a.txt", want: []Action{{Kind: ActionEdit, File: "a.txt", Force: true}}},
		{cmdline: "tabe a.txt|tabnew", want: []Action{{Kind: ActionEdit, File: "a.txt"}, {Kind: ActionTabNew}}},
		{cmdline: "tabnew", want: []Action{{Kind: ActionTabNew}}},
		{cmdline: "tabnew b.txt", want: []Action{{Kind: ActionEdit, File: "b.txt"}}},
		// 最短の長さより短い形は当たらない（Vim 9.1 の :tab は修飾子、:tabne は :tabnext）。
		{cmdline: "tab a.txt", wantCode: "E492"},
		{cmdline: "tabne", wantCode: "E492"},
		{cmdline: "tabnewx", wantCode: "E492"},
		// 範囲は付け替えた先の :edit と同じく取らない。
		{cmdline: "2tabe a.txt", wantCode: "E481"},
		// 本体のコマンドは別名より先に引く。
		{cmdline: "tabm 0", want: []Action{{Kind: ActionTabMove, Count: 0, Text: "0"}}},
	}
	for _, tt := range tests {
		res, err := Execute(buffer.New("a\n"), &State{Aliases: al}, 0, tt.cmdline)
		if code := errCode(t, err); code != tt.wantCode {
			t.Errorf("%s: code = %q, want %q (err = %v)", tt.cmdline, code, tt.wantCode, err)
			continue
		}
		if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
			t.Errorf("%s: actions = %+v, want %+v", tt.cmdline, res.Actions, tt.want)
		}
	}
	if got := al.Added(); !slices.Equal(got, []RenameAlias{{Name: "tabedit", Min: 4, Target: "edit"}, {Name: "tabnew", Min: 6, Target: tabNewName}}) {
		t.Errorf("Added = %+v", got)
	}
	if got := NewAliases().Added(); len(got) != 0 {
		t.Errorf("本体の行だけの表の Added = %+v", got)
	}
}

// 足せない別名は、表を変えずに誤りを返す。
//
// req: FR-150
func TestAddAliasErrors(t *testing.T) {
	exts := []string{"finder.files", "help", "keys"}
	tests := []struct {
		name string
		a    RenameAlias
		want string
	}{
		{name: "英字でない名前", a: RenameAlias{Name: "tab1", Min: 4, Target: "edit"}, want: "英字だけ"},
		{name: "点を含む名前", a: RenameAlias{Name: "tab.edit", Min: 8, Target: "edit"}, want: "英字だけ"},
		{name: "空の名前", a: RenameAlias{Name: "", Min: 1, Target: "edit"}, want: "英字だけ"},
		{name: "名前より長い省略", a: RenameAlias{Name: "tabe", Min: 5, Target: "edit"}, want: "1 から名前の長さ 4"},
		{name: "0 の省略", a: RenameAlias{Name: "tabe", Min: 0, Target: "edit"}, want: "1 から名前の長さ 4"},
		{name: "本体のコマンドと同じ名前", a: RenameAlias{Name: "edit", Min: 4, Target: "write"}, want: "本体の Ex コマンド :edit と重なる"},
		{name: "本体のコマンドの省略形と重なる", a: RenameAlias{Name: "editor", Min: 1, Target: "write"}, want: "名前か省略形 e が本体の Ex コマンド :edit"},
		{name: "本体のコマンドの名前が別名の省略形に当たる", a: RenameAlias{Name: "tabmovex", Min: 4, Target: "edit"}, want: "本体の Ex コマンド :tabmove と重なる"},
		{name: "本体の別名と重なる", a: RenameAlias{Name: "settle", Min: 3, Target: "edit"}, want: "別名 :set と重なる"},
		{name: "先に足した別名と重なる", a: RenameAlias{Name: "tabeditor", Min: 4, Target: "edit"}, want: "別名 :tabedit と重なる"},
		{name: "拡張機能のコマンドと同じ", a: RenameAlias{Name: "helper", Min: 2, Target: "edit"}, want: "拡張機能のコマンド :help と同じ"},
		{name: "知らない付け替え先", a: RenameAlias{Name: "foo", Min: 3, Target: "nosuch"}, want: "本体の Ex コマンドの名前でも拡張機能のコマンドの ID でもない"},
		{name: "付け替え先が省略形", a: RenameAlias{Name: "foo", Min: 3, Target: "e"}, want: "省略しない名前（edit）"},
		{name: "付け替え先が本体の別名", a: RenameAlias{Name: "foo", Min: 3, Target: "set"}, want: "別名なので"},
		{name: "付け替え先が足した別名", a: RenameAlias{Name: "foo", Min: 3, Target: "tabedit"}, want: "別名なので"},
		{name: "付け替え先が拡張機能のコマンドの省略", a: RenameAlias{Name: "foo", Min: 3, Target: "finder.fil"}, want: "でもない"},
	}
	for _, tt := range tests {
		al := NewAliases()
		if err := al.Add(RenameAlias{Name: "tabedit", Min: 4, Target: "edit"}, exts); err != nil {
			t.Fatal(err)
		}
		err := al.Add(tt.a, exts)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
		if n := len(al.Added()); n != 1 {
			t.Errorf("%s: 誤りの後の行の数 = %d", tt.name, n)
		}
	}
	// 重ならない名前（共通の接頭辞が最短の長さより短い）は足せる。
	al := NewAliases()
	for _, a := range []RenameAlias{
		{Name: "tabnew", Min: 4, Target: tabNewName}, // :tabmove（tabm）と tabn は重ならない
		{Name: "tabedit", Min: 4, Target: "edit"},    // tabn と tabe は重ならない
		{Name: "ff", Min: 2, Target: "finder.files"},
		{Name: "editx", Min: 5, Target: "edit"}, // :edit の名前（edit）は editx の最短の長さより短い
	} {
		if err := al.Add(a, exts); err != nil {
			t.Errorf("%s: %v", a.Name, err)
		}
	}
}

// 拡張機能のコマンドへ付け替える別名は、付け替えた先のコマンドを、拡張機能のコマンドと同じ規則で実行する。
//
// req: FR-150
func TestAliasToExtCommand(t *testing.T) {
	cmds := map[string]string{"finder.files": "finder", "fz": "other"}
	al := NewAliases()
	for _, a := range []RenameAlias{{Name: "files", Min: 2, Target: "finder.files"}, {Name: "fza", Min: 3, Target: "finder.files"}} {
		if err := al.Add(a, []string{"finder.files", "fz"}); err != nil {
			t.Fatal(err)
		}
	}
	st := &State{Aliases: al, ExtCommand: func(id string) (string, bool) {
		e, ok := cmds[id]
		return e, ok
	}}
	ext := func(text string) Action {
		return Action{Kind: ActionExtCommand, Extension: "finder", Command: "finder.files", Text: text}
	}
	tests := []struct {
		cmdline  string
		want     []Action
		wantCode string
	}{
		{cmdline: "files", want: []Action{ext("")}},
		{cmdline: "fi a|b", want: []Action{ext("a|b")}},
		{cmdline: "fza x", want: []Action{ext("x")}},
		// 別名と重ならない拡張機能のコマンドは、そのまま呼べる。
		{cmdline: "fz y", want: []Action{{Kind: ActionExtCommand, Extension: "other", Command: "fz", Text: "y"}}},
		{cmdline: "2files", wantCode: "E481"},
		{cmdline: "files!", wantCode: "E477"},
		{cmdline: "g/a/files", wantCode: "E319"},
		{cmdline: "w|files", wantCode: "E319"},
	}
	for _, tt := range tests {
		res, err := Execute(buffer.New("a\n"), st, 0, tt.cmdline)
		if code := errCode(t, err); code != tt.wantCode {
			t.Errorf("%s: code = %q, want %q (err = %v)", tt.cmdline, code, tt.wantCode, err)
			continue
		}
		if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
			t.Errorf("%s: actions = %+v, want %+v", tt.cmdline, res.Actions, tt.want)
		}
	}
	// 拡張機能のコマンドが呼べないとき（拡張機能の口が無い）は、知らないコマンドと同じ E492 にする。
	if _, err := Execute(buffer.New("a\n"), &State{Aliases: al}, 0, "files"); errCode(t, err) != "E492" {
		t.Errorf("ExtCommand が nil: err = %v", err)
	}
}

// :tab.new は、引数が無ければ空のタブを開く動作を、ファイルがあれば :e と同じ動作を返す。名前は省略できない。
//
// req: FR-151
func TestTabNew(t *testing.T) {
	tests := []struct {
		cmdline  string
		want     []Action
		wantCode string
	}{
		{cmdline: "tab.new", want: []Action{{Kind: ActionTabNew}}},
		{cmdline: "tab.new!", want: []Action{{Kind: ActionTabNew}}},
		{cmdline: "tab.new a\\ b.txt", want: []Action{{Kind: ActionEdit, File: "a b.txt"}}},
		{cmdline: "tab.new! a.txt", want: []Action{{Kind: ActionEdit, File: "a.txt", Force: true}}},
		{cmdline: "tab.new|tab.new", want: []Action{{Kind: ActionTabNew}, {Kind: ActionTabNew}}},
		{cmdline: "tab.ne", wantCode: "E492"},
		{cmdline: "2tab.new", wantCode: "E481"},
		{cmdline: "tab.new !ls", wantCode: "E319"},
		{cmdline: "g/b/tab.new", wantCode: "E319"},
	}
	for _, tt := range tests {
		// 未保存の変更があっても、新しいタブで開くので E37 にしない。
		b := buffer.New("a\n")
		if _, err := Execute(b, nil, 0, "s/a/b/"); err != nil {
			t.Fatal(err)
		}
		res, err := Execute(b, nil, 0, tt.cmdline)
		if code := errCode(t, err); code != tt.wantCode {
			t.Errorf("%s: code = %q, want %q (err = %v)", tt.cmdline, code, tt.wantCode, err)
			continue
		}
		if err == nil && !reflect.DeepEqual(res.Actions, tt.want) {
			t.Errorf("%s: actions = %+v, want %+v", tt.cmdline, res.Actions, tt.want)
		}
	}
	if !slices.Contains(Names(nil), Name{Full: tabNewName, Short: tabNewName}) {
		t.Errorf("Names に %s が無い", tabNewName)
	}
}

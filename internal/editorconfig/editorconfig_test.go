package editorconfig

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"testing"
)

// glob は、EditorConfig の仕様（https://spec.editorconfig.org/ の Glob Expressions）の書き方の例に当たる。
//
// req: FR-164
func TestMatch(t *testing.T) {
	tests := []struct {
		glob, name string
		want       bool
	}{
		// * は / 以外の 0 文字以上
		{"*.py", "a.py", true},
		{"*.py", ".py", true},
		{"*.py", "a.pyc", false},
		{"*.py", "dir/a.py", false},
		{"*", "Makefile", true},
		// ** は / を含む 0 文字以上
		{"lib/**.js", "lib/a.js", true},
		{"lib/**.js", "lib/x/y.js", true},
		{"lib/**.js", "src/a.js", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/xb", false},
		// ? は / 以外の 1 文字
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"a?c", "a/c", false},
		// [seq] と [!seq]
		{"[abc].txt", "b.txt", true},
		{"[abc].txt", "d.txt", false},
		{"[!abc].txt", "d.txt", true},
		{"[!abc].txt", "a.txt", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
		{"[!a]", "/", false},
		// {s1,s2,s3}。入れ子にできる
		{"*.{js,py}", "a.js", true},
		{"*.{js,py}", "a.py", true},
		{"*.{js,py}", "a.rb", false},
		{"{a,{b,c}}.txt", "c.txt", true},
		{"{a,{b,c}}.txt", "d.txt", false},
		{"{,a}b", "b", true},
		{"{,a}b", "ab", true},
		// , を含まない {s} は文字どおり
		{"{single}.b", "{single}.b", true},
		{"{single}.b", "single.b", false},
		// {num1..num2}
		{"file{1..3}.txt", "file1.txt", true},
		{"file{1..3}.txt", "file3.txt", true},
		{"file{1..3}.txt", "file4.txt", false},
		{"file{1..3}.txt", "file.txt", false},
		{"{-3..3}", "-2", true},
		{"{-3..3}", "4", false},
		{"{10..120}", "99", true},
		// \ は次の文字を文字どおりにする
		{`\*.txt`, "*.txt", true},
		{`\*.txt`, "a.txt", false},
		{`\{a,b\}`, "{a,b}", true},
		// 閉じていない [ と { は文字どおり
		{"[abc", "[abc", true},
		{"{abc", "{abc", true},
		// 多バイト文字
		{"あ?.md", "あい.md", true},
	}
	for _, tt := range tests {
		if got := Match(tt.glob, tt.name); got != tt.want {
			t.Errorf("Match(%q, %q) = %v, want %v", tt.glob, tt.name, got, tt.want)
		}
	}
}

// セクションの名前に / が無ければどの深さのファイルにも当て、/ があれば .editorconfig のあるディレクトリからのパスとして当てる。
//
// req: FR-164
func TestSectionMatches(t *testing.T) {
	tests := []struct {
		glob, rel string
		want      bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "x/y/a.go", true},
		{"Makefile", "sub/Makefile", true},
		{"/sub/*.go", "sub/a.go", true},
		{"/sub/*.go", "x/sub/a.go", false},
		{"sub/*.go", "sub/a.go", true},
		{"sub/*.go", "x/sub/a.go", false},
		{"**/sub/*.go", "x/sub/a.go", true},
		{"[a/b].go", "a.go", false},
	}
	for _, tt := range tests {
		if got := sectionMatches(tt.glob, tt.rel); got != tt.want {
			t.Errorf("sectionMatches(%q, %q) = %v, want %v", tt.glob, tt.rel, got, tt.want)
		}
	}
}

// 項目の名前と値は小文字にし、前文の root を読み、コメントと読めない行は読み飛ばす。後に書いたセクションの値が勝つ。
//
// req: FR-164
func TestParse(t *testing.T) {
	f := Parse([]byte("# comment\nroot = TRUE\n\n[*]\nIndent_Style = Space\nindent_size=4\n; comment\nbroken line\n[*.go]\nindent_style = tab\n"))
	if !f.Root {
		t.Error("Root = false")
	}
	got := f.Properties("a.go")
	want := Properties{"indent_style": "tab", "indent_size": "4"}
	if !maps.Equal(got, want) {
		t.Errorf("Properties(a.go) = %v, want %v", got, want)
	}
	got = f.Properties("a.py")
	want = Properties{"indent_style": "space", "indent_size": "4"}
	if !maps.Equal(got, want) {
		t.Errorf("Properties(a.py) = %v, want %v", got, want)
	}
	// root はセクションの中に書いても前文の root にならない。
	if f := Parse([]byte("[*]\nroot = true\n")); f.Root {
		t.Error("セクションの中の root を読んだ")
	}
}

// 探し方: 近いファイルの値が勝ち、root = true のファイルで止まり、入れ子のディレクトリのファイルも読む。unset はそのまま返す。
//
// req: FR-164
func TestLookup(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 一番外のファイルは root でないが、その中の top が root なので読まれない。
	write(FileName, "[*]\ntab_width = 3\nend_of_line = crlf\n")
	write("top/"+FileName, "root = true\n[*]\nindent_style = space\nindent_size = 4\ntab_width = 8\n[*.go]\nindent_style = tab\n")
	write("top/sub/"+FileName, "[*.go]\nindent_size = 2\n[*.md]\nindent_size = unset\n")
	write("top/sub/deep/"+FileName, "[deep/*.go]\ntab_width = 5\n[x.go]\ntab_width = 6\n")
	tests := []struct {
		rel  string
		want Properties
	}{
		{"top/a.py", Properties{"indent_style": "space", "indent_size": "4", "tab_width": "8"}},
		{"top/a.go", Properties{"indent_style": "tab", "indent_size": "4", "tab_width": "8"}},
		{"top/sub/a.go", Properties{"indent_style": "tab", "indent_size": "2", "tab_width": "8"}},
		{"top/sub/a.md", Properties{"indent_style": "space", "indent_size": "unset", "tab_width": "8"}},
		// deep の中の [deep/*.go] は、deep の .editorconfig からのパス（x.go）に当たらない。
		{"top/sub/deep/x.go", Properties{"indent_style": "tab", "indent_size": "2", "tab_width": "6"}},
		{"other.txt", Properties{"tab_width": "3", "end_of_line": "crlf"}},
	}
	for _, tt := range tests {
		got, err := Lookup(filepath.Join(dir, tt.rel))
		if err != nil {
			t.Errorf("Lookup(%s): %v", tt.rel, err)
		}
		// 一時ディレクトリの外（/tmp など）の .editorconfig の項目は比べない。
		for k := range got {
			if _, ok := tt.want[k]; !ok {
				delete(got, k)
			}
		}
		if !maps.Equal(got, tt.want) {
			t.Errorf("Lookup(%s) = %v, want %v", tt.rel, got, tt.want)
		}
	}
}

// 読めない .editorconfig は読み飛ばし、ほかのファイルの項目を返して、誤りを返す。
//
// req: FR-164
func TestLookupReadError(t *testing.T) {
	denied := errors.New("permission denied")
	files := map[string]string{
		"/p/" + FileName:   "root = true\n[*]\nindent_size = 2\n",
		"/p/q/" + FileName: "",
	}
	orig := readFile
	readFile = func(name string) ([]byte, error) {
		if name == "/p/q/"+FileName {
			return nil, denied
		}
		if s, ok := files[name]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
	t.Cleanup(func() { readFile = orig })
	got, err := Lookup("/p/q/a.txt")
	if !errors.Is(err, denied) {
		t.Errorf("err = %v, want %v", err, denied)
	}
	if want := (Properties{"indent_size": "2"}); !maps.Equal(got, want) {
		t.Errorf("Lookup = %v, want %v", got, want)
	}
}

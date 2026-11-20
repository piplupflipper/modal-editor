package editor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stdinState は、カーソルのあるバッファの、標準入力のバッファ（FR-158）として確かめる性質である。
type stdinState struct {
	path     string
	text     string
	noLines  bool
	eol      bool
	modified bool
	readOnly bool
	undo     bool
}

func (h *harness) stdinState() stdinState {
	var s stdinState
	h.e.do(func() {
		b := h.e.cur.buf
		s = stdinState{
			path: h.e.cur.path, text: b.Text(), noLines: b.NoLines(), eol: b.HasTrailingNewline(),
			modified: b.Modified(), readOnly: b.ReadOnly(), undo: b.CanUndo(),
		}
	})
	return s
}

// 起動の引数の - で読んだ中身は、名前の無いバッファになる。行の分け方と末尾の改行と正しくない UTF-8 の扱いは、ファイルを読むときと同じで、
// :w ファイル で読んだとおりのバイト列を書き出す（末尾の改行が無ければ、ファイルと同じく fixendofline で補う）。
// 中身が空でなく -R でもなければ未保存の変更がある扱いで、:q は E37 で断られ、u では読んだ中身を消さない。
// 空の入力と改行 1 つの入力と -R は、Vim 9.1 と同じく変更の無い扱いで、-R は編集を禁止する（FR-119）。
//
// req: FR-158
func TestOpenStdin(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		readOnly bool
		want     stdinState
		written  string
	}{
		{"末尾の改行あり", "hoge\nあいう\n", false, stdinState{text: "hoge\nあいう", eol: true, modified: true}, "hoge\nあいう\n"},
		{"末尾の改行なし", "a\nb", false, stdinState{text: "a\nb", modified: true}, "a\nb\n"},
		{"正しくない UTF-8", "a\xffb\n\xe3\x81\n", false, stdinState{text: "a\xffb\n\xe3\x81", eol: true, modified: true}, "a\xffb\n\xe3\x81\n"},
		{"空の入力", "", false, stdinState{noLines: true, eol: true}, ""},
		{"改行 1 つ", "\n", false, stdinState{eol: true}, "\n"},
		{"-R", "x\n", true, stdinState{text: "x", eol: true, readOnly: true}, "x\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			h := startWith(t, Config{WorkDir: dir}, func(e *Editor) error {
				e.OpenStdin(tt.in, tt.readOnly)
				return nil
			})
			if got := h.stdinState(); got != tt.want {
				t.Errorf("開いた直後 = %+v, want %+v", got, tt.want)
			}
			if tt.want.modified {
				// 変更の無いバッファの :q はエディタを終わらせるので、変更があるときだけ確かめる。
				h.keys(":q<CR>")
				h.settle()
				if msg := h.message(); !strings.HasPrefix(msg, "E37") {
					t.Errorf(":q のメッセージ = %q, want E37", msg)
				}
			}
			h.keys("u")
			h.settle()
			if got := h.stdinState(); got.text != tt.want.text || got.modified != tt.want.modified {
				t.Errorf("u の後 = %+v, want 読んだ中身と変更の扱いのまま", got)
			}
			out := filepath.Join(dir, "out.txt")
			h.keys(":w " + out + "<CR>")
			h.settle()
			if got, err := os.ReadFile(out); err != nil || string(got) != tt.written {
				t.Errorf(":w で書き出した中身 = %q, %v, want %q（メッセージ %q）", got, err, tt.written, h.message())
			}
			if got := h.stdinState(); got.path != out || got.modified {
				t.Errorf(":w の後 = %+v, want 名前が %s で変更の無いバッファ", got, out)
			}
		})
	}
}

// 編集を禁止した - のバッファ（-R）は、x を E21 で断る（FR-119）。
//
// req: FR-158
func TestOpenStdinReadOnly(t *testing.T) {
	h := startWith(t, Config{}, func(e *Editor) error {
		e.OpenStdin("abc\n", true)
		return nil
	})
	h.keys("x")
	h.settle()
	if got, msg := h.text(), h.message(); got != "abc" || !strings.HasPrefix(msg, "E21") {
		t.Errorf("x: text %q, message %q", got, msg)
	}
}

// 既にある server に繋いだ client（Attach）の - の中身は、その client のタブの名前の無いバッファになり、未保存の変更がある扱いになる。
// 待つだけの client（Wait。:! の中の vi-imitation -）の中身は、:! を実行した client のタブになる。
//
// req: FR-158
func TestStdinOfAttachedClients(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	h := start(t, Config{WorkDir: dir}, "a.txt")
	text := "piped\n"
	b := h.attach(ClientInfo{ID: 2, WorkDir: dir, Stdin: &text})
	var got stdinState
	var owner ClientID
	var tabs int
	h.e.do(func() {
		c := h.e.findClient(2)
		tabs = len(c.tabs)
		bs := c.tabs[0]
		owner = bs.owner.info.ID
		got = stdinState{path: bs.path, text: bs.buf.Text(), eol: bs.buf.HasTrailingNewline(), modified: bs.buf.Modified()}
	})
	if want := (stdinState{text: "piped", eol: true, modified: true}); got != want || owner != 2 || tabs != 1 {
		t.Errorf("Attach の - のタブ = %+v（client %d、タブ %d 個）, want %+v の client 2 のタブ 1 個", got, owner, tabs, want)
	}
	b.keys(":q<CR>")
	h.settle()
	if exits := b.term.exits(); len(exits) != 0 {
		t.Errorf("未保存の - のバッファで :q した client が終わった: %q", exits)
	}

	waited := "from wait\n"
	done := make(chan string, 1)
	if _, err := h.e.Wait(context.Background(), WaitInfo{Stdin: &waited, Parent: 2, Done: func(r string) { done <- r }}); err != nil {
		t.Fatal(err)
	}
	h.e.do(func() {
		c := h.e.findClient(2)
		tabs = len(c.tabs)
		h.e.withView(c, func() { got = stdinState{text: h.e.cur.buf.Text(), modified: h.e.cur.buf.Modified()} })
	})
	if want := (stdinState{text: "from wait", modified: true}); got != want || tabs != 2 {
		t.Errorf("Wait の - のタブ = %+v（タブ %d 個）, want %+v のタブが足される", got, tabs, want)
	}
	b.keys(":q!<CR>")
	select {
	case r := <-done:
		if r != "" {
			t.Errorf("Done(%q), want empty", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("- のタブを閉じても待つだけの client が終わらない")
	}
}

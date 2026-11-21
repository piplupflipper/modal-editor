package command

import (
	"errors"
	"fmt"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// setupComment は、text（| はカーソル）を内容とし、comment.string を cms にした State を作る。
func setupComment(t *testing.T, text, cms string) *State {
	t.Helper()
	s := setup(t, text)
	o := fakeOptions{vs: &option.Values{}}
	o.set(t, option.CommentString, option.StringValue(cms))
	s.Ex.Options = o
	return s
}

// gc、gcc、オペレーターの後の gc は、Neovim 0.12.5 の組み込みの gc と同じテキストとカーソルになる（commentCases）。
//
// req: FR-169
func TestCommentNvim(t *testing.T) {
	for _, tc := range commentCases {
		t.Run(fmt.Sprintf("%q/%q/%s", tc.text, tc.cms, tc.keys), func(t *testing.T) {
			s := setupComment(t, tc.text, tc.cms)
			feedKeys(s, tc.keys)
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if got := s.Cursor().String(); got != tc.cursor {
				t.Errorf("cursor = %s, want %s", got, tc.cursor)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("mode = %v", s.Mode())
			}
		})
	}
}

// ノーマルモードの <C-_> と <C-/> は gcc と、ビジュアルモードの <C-_> と <C-/> は gc と同じに動く（オーナーの nvim の割り当て）。
// 回数、ドットリピート、アンドゥも同じである。
//
// req: FR-169
func TestCommentCtrlSlash(t *testing.T) {
	for _, key := range []string{"<C-_>", "<C-/>"} {
		for _, tt := range []struct{ text, keys, want string }{
			{"  foo|bar", "%s", "  // foobar"},
			{"a\nb|c\nd\ne", "2%s", "a\n// bc\n// d\ne"},
			{"a\nb|c\nd\ne", "%sj.", "a\n// bc\n// d\ne"},
			{"a\nb|c\nd", "%su", "a\nbc\nd"},
			{"a\nb|c\nd", "Vj%s", "a\n// bc\n// d"},
			{"a\n// b|c\n// d", "vj%s", "a\nbc\nd"},
		} {
			keys := fmt.Sprintf(tt.keys, key)
			s := setupComment(t, tt.text, "// %s")
			feedKeys(s, keys)
			if got := s.buf.Text(); got != tt.want {
				t.Errorf("%q の %s = %q, want %q", tt.text, keys, got, tt.want)
			}
			if s.Mode() != ModeNormal {
				t.Errorf("%q の %s の後の mode = %v", tt.text, keys, s.Mode())
			}
		}
	}
}

// comment.string が空なら、Neovim 0.12.5 と同じく警告を出し、前と後ろを空として続ける（空白だけの行は空の行になり、ほかの行は変わらない）。
// Neovim は何も変わらなくてもバッファを変更ありにするが、本体は変わった行だけを書き換えるので、変わらなければアンドゥの単位も作らない。
// オペレーターの後の gc は、空の形ではどの行にも当たらないとして失敗にする（Neovim はバッファの全体を選ぶ）。
//
// req: FR-169
func TestCommentEmptyString(t *testing.T) {
	s := setupComment(t, "  |a\n   \nb", "")
	rs := feedKeys(s, "3gcc")
	if got, want := s.buf.Text(), "  a\n\nb"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	if m := rs[len(rs)-1].Message; m != (Message{Text: commentEmptyMessage, Warning: true}) {
		t.Errorf("message = %+v", m)
	}

	s = setupComment(t, "|a", "")
	v := s.buf.Version()
	rs = feedKeys(s, "gcc")
	if s.buf.Text() != "a" || s.buf.Version() != v {
		t.Errorf("text = %q, version %d → %d", s.buf.Text(), v, s.buf.Version())
	}
	if rs[len(rs)-1].Message.Text != commentEmptyMessage || rs[len(rs)-1].Err != nil {
		t.Errorf("result = %+v", rs[len(rs)-1])
	}

	for _, cms := range []string{"", "%s", " %s "} {
		s = setupComment(t, "a\n|b\n\nc", cms)
		rs = feedKeys(s, "dgc")
		if got := s.buf.Text(); got != "a\nb\n\nc" {
			t.Errorf("cms %q の dgc = %q", cms, got)
		}
		if !errors.Is(rs[len(rs)-1].Err, ErrFailed) {
			t.Errorf("cms %q の dgc の err = %v", cms, rs[len(rs)-1].Err)
		}
	}
}

// comment.string は使うたびにバッファの今の値を読むので、途中で変えた値が次の gc から効く。
// 設定の口が無ければ、表の既定値（空）を使う。
//
// req: FR-169
func TestCommentStringLive(t *testing.T) {
	s := setupComment(t, "|a", "// %s")
	feedKeys(s, "gcc")
	o := s.Ex.Options.(fakeOptions)
	o.set(t, option.CommentString, option.StringValue("# %s"))
	feedKeys(s, "gcc")
	if got, want := s.buf.Text(), "# // a"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}

	s = setup(t, "|a")
	rs := feedKeys(s, "gcc")
	if s.buf.Text() != "a" || rs[len(rs)-1].Message.Text != commentEmptyMessage {
		t.Errorf("設定の口が無いときの gcc: text = %q, result = %+v", s.buf.Text(), rs[len(rs)-1])
	}
}

// gc はテキストを変えるので、編集を禁止したバッファでは断る（FR-85）。
//
// req: FR-169
func TestCommentReadOnly(t *testing.T) {
	for _, keys := range []string{"gcc", "gcj", "<C-_>", "Vgc"} {
		s := setupComment(t, "|a\nb", "// %s")
		s.buf.SetReadOnly(true)
		rs := feedKeys(s, keys)
		if !errors.Is(rs[len(rs)-1].Err, buffer.ErrReadOnly) || s.buf.Text() != "a\nb" {
			t.Errorf("%s: text = %q, err = %v", keys, s.buf.Text(), rs[len(rs)-1].Err)
		}
	}
}

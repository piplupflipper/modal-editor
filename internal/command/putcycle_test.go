package command

import (
	"errors"
	"slices"
	"testing"
)

// cycleState は、"|xy" のバッファと、新しい順に c、b、a の履歴を持つ State を作る。
func cycleState(t *testing.T, text string) *State {
	t.Helper()
	s := setup(t, text)
	s.registers.history = []Register{{Text: "a", filled: true}, {Text: "b", filled: true}, {Text: "c", filled: true}}
	return s
}

// lastErr は、結果のうち最後の誤りを返す。
func lastErr(rs []Result) error {
	var err error
	for _, r := range rs {
		if r.Err != nil {
			err = r.Err
		}
	}
	return err
}

// 貼り付けの直後の <C-p> は、貼ったテキストを履歴の 1 つ古い項目に置き換え、続けるとさらに古い項目に置き換える。
// <C-n> は新しいほうへ戻る。端では失敗するが、反対の向きには続けられる。置き換えは元の貼り付けと 1 つのアンドゥの単位で、
// 履歴の順は変えない。
//
// req: FR-55
func TestPutCycle(t *testing.T) {
	s := cycleState(t, "|xy")
	steps := []struct {
		keys, text string
		col        int
		fail       bool
	}{
		{"p", "xcy", 1, false},
		{"<C-p>", "xby", 1, false},
		{"<C-p>", "xay", 1, false},
		{"<C-p>", "xay", 1, true},
		{"<C-n>", "xby", 1, false},
		{"<C-n>", "xcy", 1, false},
		{"<C-n>", "xcy", 1, true},
		{"2<C-p>", "xay", 1, false},
		{"u", "xy", 0, false},
	}
	for _, st := range steps {
		err := lastErr(feedKeys(s, st.keys))
		if (err != nil) != st.fail {
			t.Fatalf("%s: err = %v", st.keys, err)
		}
		if st.fail && !errors.Is(err, ErrFailed) {
			t.Errorf("%s: err = %v, want ErrFailed", st.keys, err)
		}
		if got := s.buf.Text(); got != st.text {
			t.Fatalf("%s: text = %q, want %q", st.keys, got, st.text)
		}
		if got := s.Cursor(); got != pos(0, st.col) {
			t.Errorf("%s: cursor = %v", st.keys, got)
		}
	}
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("履歴 = %q（置き換えで順を変えた）", got)
	}
	if s.buf.CanUndo() {
		t.Error("u の 1 回で貼り付けの前に戻らない")
	}
}

// P、行単位のテキスト、回数つきの貼り付けも、同じ向きと回数で置き換える。
//
// req: FR-55
func TestPutCycleShapes(t *testing.T) {
	cases := []struct {
		name, text, keys, want string
		history                []Register // 古い順
	}{
		{"P", "x|y", "P<C-p>", "xby", nil},
		{"回数", "|xy", "2p<C-p>", "xbby", nil},
		{"行単位の p", "|one\ntwo", "p<C-p>", "one\nB1\nB2\ntwo", []Register{{Text: "B1\nB2", Linewise: true, filled: true}, {Text: "C", Linewise: true, filled: true}}},
		{"行単位の P", "one\n|two", "P<C-p>", "one\nB1\nB2\ntwo", []Register{{Text: "B1\nB2", Linewise: true, filled: true}, {Text: "C", Linewise: true, filled: true}}},
		{"行単位から文字単位へ", "|xy", "p<C-p>", "xby", []Register{{Text: "b", filled: true}, {Text: "L", Linewise: true, filled: true}}},
		{"空のテキストは飛ばす", "|xy", "p<C-p>", "xay", []Register{{Text: "a", filled: true}, {filled: true}, {Text: "c", filled: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cycleState(t, tc.text)
			if tc.history != nil {
				s.registers.history = tc.history
			}
			if err := lastErr(feedKeys(s, tc.keys)); err != nil {
				t.Fatal(err)
			}
			if got := s.buf.Text(); got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			feedKeys(s, "u")
			if got, want := s.buf.Text(), setup(t, tc.text).buf.Text(); got != want {
				t.Errorf("u の後 = %q, want %q", got, want)
			}
		})
	}
}

// 名前を指定した貼り付けの直後の <C-p> は履歴の先頭に置き換え、<C-n> で元のレジスターの内容に戻る。
//
// req: FR-55
func TestPutCycleNamed(t *testing.T) {
	s := cycleState(t, "|xy")
	s.registers.named[0] = Register{Text: "A", filled: true}
	for _, st := range []struct{ keys, text string }{
		{`"ap`, "xAy"}, {"<C-p>", "xcy"}, {"<C-p>", "xby"}, {"<C-n>", "xcy"}, {"<C-n>", "xAy"},
	} {
		if err := lastErr(feedKeys(s, st.keys)); err != nil {
			t.Fatalf("%s: %v", st.keys, err)
		}
		if got := s.buf.Text(); got != st.text {
			t.Fatalf("%s: text = %q, want %q", st.keys, got, st.text)
		}
	}
	if err := lastErr(feedKeys(s, "<C-n>")); !errors.Is(err, ErrFailed) {
		t.Errorf("元のレジスターより新しいほうへ進めた: %v", err)
	}
}

// 貼り付けの後に別の操作を挟むと、<C-p> と <C-n> は失敗し、バッファを変えない。貼り付けの前の <C-p> も失敗する。
//
// req: FR-55
func TestPutCycleAfterOtherCommand(t *testing.T) {
	for _, keys := range []string{"<C-p>", "<C-n>", "pl<C-p>", "pu<C-p>", "px<C-p>", "p:<Esc><C-p>", "pyl<C-p>", `p"ayl<C-p>`, "pix<Esc><C-p>"} {
		t.Run(keys, func(t *testing.T) {
			s := cycleState(t, "|xyz")
			rs := feedKeys(s, keys)
			// 最後の <C-p> か <C-n>（5 文字）の前までを打ったテキスト。最後のキーで変わっていないことを見る。
			s2 := cycleState(t, "|xyz")
			feedKeys(s2, keys[:len(keys)-5])
			before := s2.buf.Text()
			if err := rs[len(rs)-1].Err; !errors.Is(err, ErrFailed) {
				t.Errorf("err = %v, want ErrFailed", err)
			}
			if got := s.buf.Text(); got != before {
				t.Errorf("text = %q, want %q", got, before)
			}
		})
	}
}

// 端末からの貼り付けも別の操作であり、その後の <C-p> は失敗する。
//
// req: FR-55
func TestPutCycleAfterTerminalPaste(t *testing.T) {
	s := cycleState(t, "|xy")
	feedKeys(s, "p")
	s.Paste("Q")
	text := s.buf.Text()
	if err := lastErr(feedKeys(s, "<C-p>")); !errors.Is(err, ErrFailed) {
		t.Errorf("err = %v", err)
	}
	if got := s.buf.Text(); got != text {
		t.Errorf("text = %q, want %q", got, text)
	}
}

// マクロの中の <C-p> も置き換え、再生全体が 1 つのアンドゥの単位のままである。
// マクロの最後の貼り付けを、再生の外の <C-p> では置き換えない（再生の後の値は @ であり、貼り付けではない）。
//
// req: FR-55
func TestPutCycleInMacro(t *testing.T) {
	s := cycleState(t, "|xy")
	s.registers.named['q'-'a'] = Register{Text: "p\x10", filled: true}
	if err := lastErr(feedKeys(s, "@q")); err != nil {
		t.Fatal(err)
	}
	if got := s.buf.Text(); got != "xby" {
		t.Fatalf("text = %q", got)
	}
	if err := lastErr(feedKeys(s, "<C-p>")); !errors.Is(err, ErrFailed) {
		t.Errorf("再生の外の <C-p> = %v", err)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "xy" {
		t.Errorf("u の後 = %q", got)
	}
}

// . は置き換えた後も元の貼り付け（p）を繰り返し、. の直後の <C-p> も置き換える。
//
// req: FR-55
func TestPutCycleDot(t *testing.T) {
	s := cycleState(t, "|xy")
	feedKeys(s, "p<C-p>.")
	if got := s.buf.Text(); got != "xbcy" {
		t.Fatalf("text = %q", got)
	}
	feedKeys(s, "<C-p>")
	if got := s.buf.Text(); got != "xbby" {
		t.Errorf("text = %q", got)
	}
}

// 一覧の項目は、履歴を新しい順に、続けて中身のある名前付きのレジスターを a から並べる。
//
// req: FR-54
func TestRegistersList(t *testing.T) {
	var r Registers
	r.history = []Register{{Text: "old", filled: true}, {Text: "new", filled: true}}
	r.named['c'-'a'] = Register{Text: "C", filled: true}
	r.named[0] = Register{Text: "", filled: true}
	got := r.List()
	want := []RegisterEntry{
		{Index: 0, Register: r.history[1]},
		{Index: 1, Register: r.history[0]},
		{Name: 'a', Register: r.named[0]},
		{Name: 'c', Register: r.named[2]},
	}
	if !slices.EqualFunc(got, want, func(a, b RegisterEntry) bool {
		return a.Name == b.Name && a.Index == b.Index && a.Register.Text == b.Register.Text
	}) {
		t.Errorf("List = %+v", got)
	}
}

// 一覧で選んだ履歴の項目は、先頭へ移してから p と同じく貼る。直後の <C-p> は 1 つ古い項目に置き換え、. は p を繰り返す。
// 名前付きのレジスターは "ap と同じく貼り、履歴の順を変えない。一覧を作った後に履歴が変わっていれば失敗する。
//
// req: FR-54, FR-55
func TestPutFromList(t *testing.T) {
	s := cycleState(t, "|xy")
	list := s.registers.List()
	res := s.PutFromList(list[2], s.registers.Pushes())
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := s.buf.Text(); got != "xay" {
		t.Fatalf("text = %q", got)
	}
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"a", "c", "b"}) {
		t.Errorf("履歴 = %q", got)
	}
	feedKeys(s, "<C-p>")
	if got := s.buf.Text(); got != "xcy" {
		t.Errorf("<C-p> の後 = %q", got)
	}
	feedKeys(s, "u")
	if got := s.buf.Text(); got != "xy" {
		t.Errorf("u の後 = %q", got)
	}

	s = cycleState(t, "|xy")
	s.registers.named[1] = Register{Text: "B", filled: true}
	list = s.registers.List()
	if res := s.PutFromList(list[3], s.registers.Pushes()); res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := s.buf.Text(); got != "xBy" {
		t.Errorf("text = %q", got)
	}
	if got := historyTexts(&s.registers); !slices.Equal(got, []string{"c", "b", "a"}) {
		t.Errorf("履歴 = %q", got)
	}

	s = cycleState(t, "|xy")
	pushes := s.registers.Pushes()
	feedKeys(s, "yl")
	if res := s.PutFromList(RegisterEntry{Index: 1}, pushes); !errors.Is(res.Err, errListStale) {
		t.Errorf("履歴が変わった後の err = %v", res.Err)
	}
	if got := s.buf.Text(); got != "xy" {
		t.Errorf("text = %q", got)
	}
}

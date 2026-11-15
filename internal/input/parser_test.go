package input

import (
	"errors"
	"reflect"
	"testing"
)

// feed は、表記 s のキーを順に Feed し、全ての結果を返す。
func feed(p *Parser, s string) []Result {
	var out []Result
	for _, k := range ParseKeys(s) {
		out = append(out, p.Feed(k)...)
	}
	return out
}

func builtin(id string) Ref { return Ref{ID: id} }

func done(c Command) Result { return Result{Command: c, Status: StatusDone} }

// req: FR-02, FR-03, FR-06
func TestParserBuiltin(t *testing.T) {
	invalid := Result{Status: StatusInvalid}
	canceled := Result{Status: StatusCanceled}
	tests := []struct {
		name string
		keys string
		want []Result
	}{
		{"d2w", "d2w", []Result{done(Command{
			Kind: KindOperator, Count: 2, CountGiven: true, Operator: builtin("delete"),
			Target: Target{Kind: TargetMotion, Ref: builtin("word-next")},
		})}},
		{"回数は前と後の積", "2d3w", []Result{done(Command{
			Kind: KindOperator, Count: 6, CountGiven: true, Operator: builtin("delete"),
			Target: Target{Kind: TargetMotion, Ref: builtin("word-next")},
		})}},
		{"レジスターと重ね押し", `"ayy`, []Result{done(Command{
			Kind: KindOperator, Register: 'a', Count: 1, Operator: builtin("yank"),
			Target: Target{Kind: TargetLine},
		})}},
		{"レジスターの前後の回数", `2"a3yy`, []Result{done(Command{
			Kind: KindOperator, Register: 'a', Count: 6, CountGiven: true, Operator: builtin("yank"),
			Target: Target{Kind: TargetLine},
		})}},
		{"テキストオブジェクト", `ci"`, []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("change"), Mode: ModeInsert,
			Target: Target{Kind: TargetTextObject, Ref: builtin("inner-double-quote")},
		})}},
		{"dd", "dd", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("delete"), Target: Target{Kind: TargetLine},
		})}},
		{"3dd", "3dd", []Result{done(Command{
			Kind: KindOperator, Count: 3, CountGiven: true, Operator: builtin("delete"),
			Target: Target{Kind: TargetLine},
		})}},
		{"cc はインサートモードへ", "cc", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("change"), Mode: ModeInsert,
			Target: Target{Kind: TargetLine},
		})}},
		{"<< の < は文字", "<<", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("dedent"), Target: Target{Kind: TargetLine},
		})}},
		{"fx", "fx", []Result{done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("find-forward"), Char: 'x'},
		})}},
		{"f の引数の数字は回数でない", "2f3", []Result{done(Command{
			Kind: KindMotion, Count: 2, CountGiven: true,
			Target: Target{Kind: TargetMotion, Ref: builtin("find-forward"), Char: '3'},
		})}},
		{"dtx", "dtx", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("delete"),
			Target: Target{Kind: TargetMotion, Ref: builtin("till-forward"), Char: 'x'},
		})}},
		{"3j", "3j", []Result{done(Command{
			Kind: KindMotion, Count: 3, CountGiven: true, Target: Target{Kind: TargetMotion, Ref: builtin("down")},
		})}},
		{"0 はモーション", "0", []Result{done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("line-start")},
		})}},
		{"10 の 0 は回数", "10j", []Result{done(Command{
			Kind: KindMotion, Count: 10, CountGiven: true, Target: Target{Kind: TargetMotion, Ref: builtin("down")},
		})}},
		{"d0", "d0", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("delete"),
			Target: Target{Kind: TargetMotion, Ref: builtin("line-start")},
		})}},
		{"gg は 2 キーのモーション", "dgg", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("delete"),
			Target: Target{Kind: TargetMotion, Ref: builtin("file-start")},
		})}},
		{"rx", "3rx", []Result{done(Command{
			Kind: KindAction, Count: 3, CountGiven: true, Action: builtin("replace-char"), Char: 'x',
		})}},
		{"x", `"_x`, []Result{done(Command{
			Kind: KindAction, Register: '_', Count: 1, Action: builtin("delete-char"),
		})}},
		{"i はインサートモードへ", "i", []Result{done(Command{
			Kind: KindAction, Count: 1, Action: builtin("insert"), Mode: ModeInsert,
		})}},
		{": はコマンドラインモードへ", ":", []Result{done(Command{
			Kind: KindAction, Count: 1, Action: builtin("command-line"), Mode: ModeCommandLine,
		})}},

		// 不正な列
		{"dq", "dq", []Result{invalid}},
		{"dy", "dy", []Result{invalid}},
		{"g の後に無い文字", "gq", []Result{invalid}},
		{"オペレーターの後のレジスター", `d"`, []Result{invalid}},
		{"レジスターの名前でない", `"<C-a>`, []Result{invalid}},
		{"テキストオブジェクトはオペレーターの後だけ", "iw", []Result{
			done(Command{Kind: KindAction, Count: 1, Action: builtin("insert"), Mode: ModeInsert}),
			done(Command{Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("word-next")}}),
		}},
		{"f の引数に特殊キー", "f<CR>", []Result{invalid}},
		{"不正な列の後は初期状態", "dqj", []Result{invalid, done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("down")},
		})}},

		// 途中の Esc
		{"オペレーターの後の Esc", "d<Esc>", []Result{canceled}},
		{"回数の後の Esc", "3<Esc>j", []Result{canceled, done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("down")},
		})}},
		{"テキストオブジェクトの途中の Esc", "di<Esc>", []Result{canceled}},
		{"f の引数の代わりの Esc", "f<Esc>", []Result{canceled}},
		{"レジスター名の代わりの Esc", `"<Esc>`, []Result{canceled}},
		{"初期状態の Esc", "<Esc>", []Result{canceled}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(DefaultKeymap())
			got := feed(p, tt.keys)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s:\n got  %+v\n want %+v", tt.keys, got, tt.want)
			}
			if p.Pending() {
				t.Errorf("%s: 結果を返した後も解釈の途中である", tt.keys)
			}
		})
	}
}

// req: FR-02
func TestParserPending(t *testing.T) {
	p := NewParser(DefaultKeymap())
	for _, s := range []string{"2", "d", "3", "i"} {
		if rs := feed(p, s); len(rs) != 0 {
			t.Fatalf("%q の時点で結果 %+v を返した", s, rs)
		}
		if !p.Pending() {
			t.Fatalf("%q の後で Pending が false", s)
		}
	}
}

// req: FR-02
func TestParserCountClamp(t *testing.T) {
	p := NewParser(DefaultKeymap())
	rs := feed(p, "99999999999d99999999999w")
	if len(rs) != 1 || rs[0].Command.Count != MaxCount {
		t.Errorf("got %+v, want Count %d", rs, MaxCount)
	}
}

// 拡張機能の宣言（基本設計 5.1 の surround の例）と同じ操作を登録して解釈する。
//
// req: FR-11, FR-12
func TestParserExtension(t *testing.T) {
	km := DefaultKeymap()
	add := Ref{ID: "surround.add", Extension: "surround"}
	inner := Ref{ID: "surround.inner", Extension: "surround"}
	if err := km.Bind(Binding{Kind: BindOperator, Keys: ParseKeys("ys"), Ref: add}); err != nil {
		t.Fatal(err)
	}
	if err := km.Bind(Binding{Kind: BindTextObject, Keys: ParseKeys("is"), Ref: inner}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		keys string
		want []Result
	}{
		{"拡張機能のオペレーターと本体のテキストオブジェクト", "ysiw", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: add,
			Target: Target{Kind: TargetTextObject, Ref: builtin("inner-word")},
		})}},
		{"拡張機能のオペレーターの重ね押し", "2yss", []Result{done(Command{
			Kind: KindOperator, Count: 2, CountGiven: true, Operator: add, Target: Target{Kind: TargetLine},
		})}},
		{"本体のオペレーターと拡張機能のテキストオブジェクト", "dis", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("delete"),
			Target: Target{Kind: TargetTextObject, Ref: inner},
		})}},
		{"y は ys の接頭辞だが、次のキーで y に決まる", "yw", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("yank"),
			Target: Target{Kind: TargetMotion, Ref: builtin("word-next")},
		})}},
		{"yy も y に決まる", "yy", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: builtin("yank"), Target: Target{Kind: TargetLine},
		})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feed(NewParser(km), tt.keys)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s:\n got  %+v\n want %+v", tt.keys, got, tt.want)
			}
		})
	}
}

// 短い登録に決めた後、残りのキーを続けて解釈する。
//
// req: FR-02, FR-11
func TestParserShorterMatchAndFlush(t *testing.T) {
	km := DefaultKeymap()
	xy := Ref{ID: "x.y", Extension: "ext"}
	if err := km.Bind(Binding{Kind: BindAction, Keys: ParseKeys("xyz"), Ref: xy}); err != nil {
		t.Fatal(err)
	}
	delChar := done(Command{Kind: KindAction, Count: 1, Action: builtin("delete-char")})
	yankLine := done(Command{Kind: KindOperator, Count: 1, Operator: builtin("yank"), Target: Target{Kind: TargetLine}})

	p := NewParser(km)
	if got, want := feed(p, "xyy"), []Result{delChar, yankLine}; !reflect.DeepEqual(got, want) {
		t.Errorf("xyy:\n got  %+v\n want %+v", got, want)
	}
	if got, want := feed(p, "xyz"), []Result{done(Command{Kind: KindAction, Count: 1, Action: xy})}; !reflect.DeepEqual(got, want) {
		t.Errorf("xyz:\n got  %+v\n want %+v", got, want)
	}

	// x の後で止めて Flush すると x に決まる。
	if rs := feed(p, "x"); len(rs) != 0 {
		t.Fatalf("x の時点で結果 %+v を返した", rs)
	}
	if got, want := p.Flush(), []Result{delChar}; !reflect.DeepEqual(got, want) {
		t.Errorf("Flush:\n got  %+v\n want %+v", got, want)
	}
	// xy の後の Flush は、x に決めて y をオペレーターとして待つ。
	feed(p, "xy")
	if got, want := p.Flush(), []Result{delChar}; !reflect.DeepEqual(got, want) {
		t.Errorf("Flush:\n got  %+v\n want %+v", got, want)
	}
	if got, want := feed(p, "y"), []Result{yankLine}; !reflect.DeepEqual(got, want) {
		t.Errorf("Flush の後の y:\n got  %+v\n want %+v", got, want)
	}
	if rs := p.Flush(); rs != nil {
		t.Errorf("待っていないときの Flush が %+v を返した", rs)
	}
}

// req: FR-11
func TestKeymapBind(t *testing.T) {
	km := DefaultKeymap()
	ext := Ref{ID: "e", Extension: "ext"}

	if err := km.Bind(Binding{Kind: BindMotion, Keys: ParseKeys("x"), Ref: ext}); !errors.Is(err, ErrConflict) {
		t.Errorf("モーションの x: err = %v, want ErrConflict", err)
	}
	if err := km.Bind(Binding{Kind: BindTextObject, Keys: ParseKeys("w"), Ref: ext}); !errors.Is(err, ErrConflict) {
		t.Errorf("テキストオブジェクトの w: err = %v, want ErrConflict", err)
	}
	// テキストオブジェクトとオペレーターは同じ状態で引かれないので、同じキーでも登録できる。
	if err := km.Bind(Binding{Kind: BindTextObject, Keys: ParseKeys("d"), Ref: ext}); err != nil {
		t.Errorf("テキストオブジェクトの d: %v", err)
	}
	for _, b := range []Binding{
		{Kind: BindMotion, Ref: ext},
		{Kind: BindMotion, Keys: ParseKeys("q")},
		{Kind: BindMotion, Keys: ParseKeys("1q"), Ref: ext},
		{Kind: BindAction, Keys: ParseKeys(`"q`), Ref: ext},
		{Kind: bindLine, Keys: ParseKeys("q"), Ref: ext},
	} {
		if err := km.Bind(b); err == nil {
			t.Errorf("Bind(%+v) がエラーを返さない", b)
		}
	}

	// 同じ種類とキーの列は置き換える。
	if err := km.Bind(Binding{Kind: BindMotion, Keys: ParseKeys("w"), Ref: ext}); err != nil {
		t.Fatal(err)
	}
	if b, ok := km.Lookup(BindMotion, ParseKeys("w")); !ok || b.Ref != ext {
		t.Errorf("Lookup(w) = %+v, %v", b, ok)
	}
	if !km.Unbind(BindMotion, ParseKeys("w")) {
		t.Error("Unbind(w) = false")
	}
	if got := feed(NewParser(km), "w"); !reflect.DeepEqual(got, []Result{{Status: StatusInvalid}}) {
		t.Errorf("Unbind の後の w = %+v", got)
	}
	if km.Unbind(BindMotion, ParseKeys("w")) {
		t.Error("2 回目の Unbind(w) = true")
	}
}

// 本体の操作の表に誤りが無く、#9 の案の一覧を全て引ける。
//
// req: FR-03
func TestDefaultKeymap(t *testing.T) {
	km := DefaultKeymap()
	tables := map[BindingKind][]spec{
		BindMotion:     builtinMotions,
		BindOperator:   builtinOperators,
		BindTextObject: builtinTextObjects,
		BindAction:     builtinActions,
	}
	for kind, specs := range tables {
		for _, s := range specs {
			if b, ok := km.Lookup(kind, ParseKeys(s.keys)); !ok || b.Ref.ID != s.id || !b.Ref.Builtin() {
				t.Errorf("%v %q: got %+v, %v", kind, s.keys, b, ok)
			}
		}
	}
	// DefaultKeymap は呼ぶたびに別の値を返す。
	if err := km.Bind(Binding{Kind: BindMotion, Keys: ParseKeys("Q"), Ref: Ref{ID: "q"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := DefaultKeymap().Lookup(BindMotion, ParseKeys("Q")); ok {
		t.Error("DefaultKeymap が登録を共有している")
	}
}

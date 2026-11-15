package input

import (
	"errors"
	"reflect"
	"testing"
)

// feed は、表記 s のキーを順に Feed し、全ての結果を返す。
func feed(p *Parser, s string) []Result {
	var out []Result
	for _, k := range MustParseKeys(s) {
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
		{"r<CR> は改行", "r<CR>", []Result{done(Command{
			Kind: KindAction, Count: 1, Action: builtin("replace-char"), Char: '\n',
		})}},
		{"f<Tab> はタブ", "f<Tab>", []Result{done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("find-forward"), Char: '\t'},
		})}},
		{"f<Space>", "f<Space>", []Result{done(Command{
			Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("find-forward"), Char: ' '},
		})}},
		{"マクロの記録", "qa", []Result{done(Command{Kind: KindRecordStart, Count: 1, Char: 'a'})}},
		{"マクロの再生", "3@a", []Result{done(Command{Kind: KindPlay, Count: 3, CountGiven: true, Char: 'a'})}},
		{"直前のマクロの再生", "@@", []Result{done(Command{Kind: KindPlay, Count: 1, Char: '@'})}},
		{"ドットリピート", ".", []Result{done(Command{Kind: KindRepeat, Count: 1})}},
		{"回数つきのドットリピート", "3.", []Result{done(Command{Kind: KindRepeat, Count: 3, CountGiven: true})}},
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
		{"f の引数に特殊キー", "f<Up>", []Result{invalid}},
		{"r の引数に修飾キーつきの文字", "r<C-a>", []Result{invalid}},
		{"q の記録先にできないレジスター", "q_", []Result{invalid}},
		{"@ の引数に特殊キー", "@<CR>", []Result{invalid}},
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
		{"q の後の Esc", "q<Esc>", []Result{canceled}},
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

// 記録中は q だけで記録の停止になり、記録中でなければレジスターの名前を待つ。
func TestParserRecording(t *testing.T) {
	p := NewParser(DefaultKeymap())
	p.SetRecording(true)
	if got, want := feed(p, "q"), []Result{done(Command{Kind: KindRecordStop, Count: 1})}; !reflect.DeepEqual(got, want) {
		t.Errorf("記録中の q:\n got  %+v\n want %+v", got, want)
	}
	// 記録中の状態は、値を返した後や Esc の後も残る。
	feed(p, "<Esc>")
	if got, want := feed(p, "q"), []Result{done(Command{Kind: KindRecordStop, Count: 1})}; !reflect.DeepEqual(got, want) {
		t.Errorf("Esc の後の記録中の q:\n got  %+v\n want %+v", got, want)
	}
	// オペレーターの後の q は記録の停止ではない（q はモーションに無いので不正）。
	if got := feed(p, "dq"); !reflect.DeepEqual(got, []Result{{Status: StatusInvalid}}) {
		t.Errorf("記録中の dq = %+v", got)
	}
	p.SetRecording(false)
	if rs := feed(p, "q"); len(rs) != 0 {
		t.Errorf("記録中でない q が %+v を返した", rs)
	}
}

// 本体の操作の途中では、Flush は何もせず次のキーを待ち続ける。
//
// req: FR-02
func TestParserFlushKeepsBuiltinPrefix(t *testing.T) {
	tests := []struct {
		before, after string
		want          Command
	}{
		{"g", "g", Command{Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("file-start")}}},
		{"3g", "g", Command{Kind: KindMotion, Count: 3, CountGiven: true, Target: Target{Kind: TargetMotion, Ref: builtin("file-start")}}},
		{"di", "w", Command{Kind: KindOperator, Count: 1, Operator: builtin("delete"), Target: Target{Kind: TargetTextObject, Ref: builtin("inner-word")}}},
		{"ci", `"`, Command{Kind: KindOperator, Count: 1, Operator: builtin("change"), Mode: ModeInsert, Target: Target{Kind: TargetTextObject, Ref: builtin("inner-double-quote")}}},
		{`"a`, "x", Command{Kind: KindAction, Register: 'a', Count: 1, Action: builtin("delete-char")}},
		{"f", "x", Command{Kind: KindMotion, Count: 1, Target: Target{Kind: TargetMotion, Ref: builtin("find-forward"), Char: 'x'}}},
		{"d", "w", Command{Kind: KindOperator, Count: 1, Operator: builtin("delete"), Target: Target{Kind: TargetMotion, Ref: builtin("word-next")}}},
	}
	for _, tt := range tests {
		p := NewParser(DefaultKeymap())
		feed(p, tt.before)
		if rs := p.Flush(); rs != nil {
			t.Errorf("%s の後の Flush が %+v を返した", tt.before, rs)
		}
		if got, want := feed(p, tt.after), []Result{done(tt.want)}; !reflect.DeepEqual(got, want) {
			t.Errorf("%s, Flush, %s:\n got  %+v\n want %+v", tt.before, tt.after, got, want)
		}
	}
}

// 拡張機能の宣言（基本設計 5.1 の surround の例）と同じ操作を登録して解釈する。
// 拡張機能の ID、コマンドの ID、オペレーターの文字の引数が値に残る。
//
// req: FR-11, FR-12
func TestParserExtension(t *testing.T) {
	km := DefaultKeymap()
	add := Ref{ID: "surround.add", Extension: "surround"}
	inner := Ref{ID: "surround.inner", Extension: "surround"}
	if err := km.Bind(Binding{Kind: BindOperator, Keys: MustParseKeys("ys"), Ref: add, TakesChar: true}); err != nil {
		t.Fatal(err)
	}
	if err := km.Bind(Binding{Kind: BindTextObject, Keys: MustParseKeys("is"), Ref: inner}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		keys string
		want []Result
	}{
		{"拡張機能のオペレーターと本体のテキストオブジェクトと文字の引数", "ysiw)", []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: add, Char: ')',
			Target: Target{Kind: TargetTextObject, Ref: builtin("inner-word")},
		})}},
		{"対象の文字の引数とオペレーターの文字の引数", `ystx"`, []Result{done(Command{
			Kind: KindOperator, Count: 1, Operator: add, Char: '"',
			Target: Target{Kind: TargetMotion, Ref: builtin("till-forward"), Char: 'x'},
		})}},
		{"拡張機能のオペレーターの重ね押し", "2yss]", []Result{done(Command{
			Kind: KindOperator, Count: 2, CountGiven: true, Operator: add, Char: ']', Target: Target{Kind: TargetLine},
		})}},
		{"オペレーターの文字の引数の代わりの Esc", "ysiw<Esc>", []Result{{Status: StatusCanceled}}},
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
		{"y の後の Esc はオペレーターごと捨てる", "y<Esc>", []Result{{Status: StatusCanceled}}},
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
	xyz := Ref{ID: "x.y", Extension: "ext"}
	if err := km.Bind(Binding{Kind: BindAction, Keys: MustParseKeys("xyz"), Ref: xyz}); err != nil {
		t.Fatal(err)
	}
	delChar := done(Command{Kind: KindAction, Count: 1, Action: builtin("delete-char")})
	yankLine := done(Command{Kind: KindOperator, Count: 1, Operator: builtin("yank"), Target: Target{Kind: TargetLine}})
	canceled := Result{Status: StatusCanceled}

	tests := []struct {
		name  string
		keys  string
		flush bool // keys の後に Flush を呼ぶ
		want  []Result
	}{
		{"続かないキーで短い方に決まる", "xyy", false, []Result{delChar, yankLine}},
		{"長い登録", "xyz", false, []Result{done(Command{Kind: KindAction, Count: 1, Action: xyz})}},
		{"x の後の Flush は x に決める", "x", true, []Result{delChar}},
		{"xy の後の Flush は x に決め、y はオペレーターとして待つ", "xy", true, []Result{delChar}},
		{"x の後の Esc は x を実行してから取り消す（Vim と同じ）", "x<Esc>", false, []Result{delChar, canceled}},
		{"xy の後の Esc は x を実行し、y を取り消す", "xy<Esc>", false, []Result{delChar, canceled}},
		{"回数は短い方に付く", "3x<Esc>", false, []Result{
			done(Command{Kind: KindAction, Count: 3, CountGiven: true, Action: builtin("delete-char")}), canceled,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewParser(km)
			got := feed(p, tt.keys)
			if tt.flush {
				got = append(got, p.Flush()...)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s:\n got  %+v\n want %+v", tt.keys, got, tt.want)
			}
		})
	}

	// xy の Flush の後、y はオペレーターとして待っている。
	p := NewParser(km)
	feed(p, "xy")
	p.Flush()
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
	keys := MustParseKeys

	conflicts := []Binding{
		{Kind: BindMotion, Keys: keys("x"), Ref: ext},     // 本体の操作 x とノーマルモードで重なる
		{Kind: BindTextObject, Keys: keys("w"), Ref: ext}, // 本体のモーション w とオペレーターの後で重なる
		{Kind: BindOperator, Keys: keys("d"), Ref: ext},   // 本体の d を置き換えない
		{Kind: BindMotion, Keys: keys("gg"), Ref: ext},    // 同じ種類でも置き換えない
	}
	for _, b := range conflicts {
		if err := km.Bind(b); !errors.Is(err, ErrConflict) {
			t.Errorf("Bind(%v %s): err = %v, want ErrConflict", b.Kind, KeysString(b.Keys), err)
		}
	}
	// 拡張機能どうしの衝突も拒む。
	if err := km.Bind(Binding{Kind: BindOperator, Keys: keys("ys"), Ref: Ref{ID: "a", Extension: "one"}}); err != nil {
		t.Fatal(err)
	}
	if err := km.Bind(Binding{Kind: BindOperator, Keys: keys("ys"), Ref: Ref{ID: "b", Extension: "two"}}); !errors.Is(err, ErrConflict) {
		t.Errorf("拡張機能どうしの ys: err = %v, want ErrConflict", err)
	}
	// モードが重ならなければ、同じキーでも登録できる。
	if err := km.Bind(Binding{Kind: BindTextObject, Modes: MapOperatorPending, Keys: keys("d"), Ref: ext}); err != nil {
		t.Errorf("オペレーターの後だけのテキストオブジェクト d: %v", err)
	}
	if err := km.Bind(Binding{Kind: BindAction, Modes: MapInsert, Keys: keys("<C-w>"), Ref: ext}); err != nil {
		t.Errorf("インサートモードの操作: %v", err)
	}
	for _, b := range []Binding{
		{Kind: BindMotion, Ref: ext},
		{Kind: BindMotion, Keys: keys("Q")},
		{Kind: BindMotion, Keys: keys("1Q"), Ref: ext},
		{Kind: BindAction, Keys: keys(`"Q`), Ref: ext},
		{Kind: BindAction, Keys: keys("qQ"), Ref: ext},
		{Kind: BindAction, Keys: keys("@Q"), Ref: ext},
		{Kind: BindAction, Keys: keys(".Q"), Ref: ext},
		{Kind: BindTextObject, Modes: MapNormal, Keys: keys("Q"), Ref: ext},
		{Kind: BindOperator, Modes: MapOperatorPending, Keys: keys("Q"), Ref: ext},
		{Kind: bindLine, Keys: keys("Q"), Ref: ext},
	} {
		if err := km.Bind(b); err == nil || errors.Is(err, ErrConflict) {
			t.Errorf("Bind(%+v): err = %v, want 衝突以外のエラー", b, err)
		}
	}
	// インサートモードの q は先に読まれないので登録できる。
	if err := km.Bind(Binding{Kind: BindAction, Modes: MapInsert, Keys: keys("qq"), Ref: ext}); err != nil {
		t.Errorf("インサートモードの qq: %v", err)
	}

	// 置き換えは Unbind してから Bind する（利用者の設定による上書き）。
	if !km.Unbind(MapNormal|MapVisual|MapOperatorPending, keys("w")) {
		t.Fatal("Unbind(w) = false")
	}
	if err := km.Bind(Binding{Kind: BindMotion, Keys: keys("w"), Ref: ext}); err != nil {
		t.Fatal(err)
	}
	if b, ok := km.Lookup(MapNormal, keys("w")); !ok || b.Ref != ext {
		t.Errorf("Lookup(w) = %+v, %v", b, ok)
	}
	// 一部のモードだけ取り除く。
	if !km.Unbind(MapNormal, keys("w")) {
		t.Error("Unbind(normal, w) = false")
	}
	if got := feed(NewParser(km), "w"); !reflect.DeepEqual(got, []Result{{Status: StatusInvalid}}) {
		t.Errorf("Unbind の後の w = %+v", got)
	}
	if got := feed(NewParser(km), "dw"); len(got) != 1 || got[0].Command.Target.Ref != ext {
		t.Errorf("オペレーターの後の w = %+v", got)
	}
	if km.Unbind(MapNormal, keys("w")) {
		t.Error("2 回目の Unbind(normal, w) = true")
	}
}

// 本体の操作の表に誤りが無く、#9 の案の一覧を全て引ける。
//
// req: FR-03
func TestDefaultKeymap(t *testing.T) {
	km := DefaultKeymap()
	tables := []struct {
		mode  MapMode
		kind  BindingKind
		specs []spec
	}{
		{MapNormal, BindMotion, builtinMotions},
		{MapOperatorPending, BindMotion, builtinMotions},
		{MapNormal, BindOperator, builtinOperators},
		{MapOperatorPending, BindTextObject, builtinTextObjects},
		{MapNormal, BindAction, builtinActions},
	}
	for _, tt := range tables {
		for _, s := range tt.specs {
			b, ok := km.Lookup(tt.mode, MustParseKeys(s.keys))
			if !ok || b.Kind != tt.kind || b.Ref.ID != s.id || !b.Ref.Builtin() {
				t.Errorf("%v %q: got %+v, %v", tt.kind, s.keys, b, ok)
			}
		}
	}
	// DefaultKeymap は呼ぶたびに別の値を返す。
	if err := km.Bind(Binding{Kind: BindMotion, Keys: MustParseKeys("Q"), Ref: Ref{ID: "q"}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := DefaultKeymap().Lookup(MapNormal, MustParseKeys("Q")); ok {
		t.Error("DefaultKeymap が登録を共有している")
	}
}

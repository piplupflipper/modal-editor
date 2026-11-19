package input

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/editcmd"
)

// 設定の keymap の右辺の操作の ID を、モードに合わせて操作の表の登録にする。使えない ID とモードと左辺は誤りにする。
//
// req: FR-126
func TestUserBinding(t *testing.T) {
	k := editcmd.MustParseKeys
	for _, tt := range []struct {
		mode MapMode
		keys string
		id   string
		kind BindingKind
		ext  string
	}{
		{MapNormal, "<C-a>", "line-start", BindMotion, ""},
		{MapVisual, "<C-a>", "line-start", BindMotion, ""},
		{MapNormal, "Q", "find-forward", BindMotion, ""},
		{MapNormal, "Q", "delete", BindOperator, ""},
		{MapVisual, "Q", "inner-word", BindTextObject, ""},
		{MapNormal, "Q", "put-after", BindAction, ""},
		// オペレーターの g~ と操作の ~ は別の ID である（#257 のレビューの必須 1）。
		{MapNormal, "Q", "toggle-case", BindOperator, ""},
		{MapVisual, "Q", "toggle-case", BindOperator, ""},
		{MapNormal, "Q", "toggle-case-char", BindAction, ""},
		{MapNormal, "Q", "hover", BindAction, ProviderExtension},
		{MapVisual, "Q", "format", BindOperator, ProviderExtension},
		{MapInsert, "<C-a>", "insert.line-start", BindAction, ""},
		{MapInsert, "<C-k>", "insert.delete-to-line-end", BindAction, ""},
		{MapInsert, "<C-w>", "insert.delete-prev-word", BindAction, ""},
		{MapInsert, "<C-u>", "insert.delete-to-line-start", BindAction, ""},
	} {
		b, err := UserBinding(tt.mode, k(tt.keys), tt.id)
		if err != nil {
			t.Errorf("UserBinding(%s, %s, %s): %v", ModeName(tt.mode), tt.keys, tt.id, err)
			continue
		}
		if b.Kind != tt.kind || b.Modes != tt.mode || b.Ref.ID != tt.id || b.Ref.Extension != tt.ext || !b.User || b.Description == "" ||
			editcmd.KeysString(b.Keys) != tt.keys {
			t.Errorf("UserBinding(%s, %s, %s) = %+v", ModeName(tt.mode), tt.keys, tt.id, b)
		}
	}
	if b, _ := UserBinding(MapNormal, k("Q"), "find-forward"); !b.TakesChar {
		t.Errorf("find-forward の TakesChar = false")
	}
	if b, _ := UserBinding(MapNormal, k("Q"), "insert"); b.NextMode != editcmd.ModeInsert {
		t.Errorf("insert の NextMode = %v", b.NextMode)
	}
	for _, tt := range []struct {
		mode     MapMode
		keys, id string
		want     string
	}{
		{MapNormal, "Q", "no-such-action", "未知の操作の ID"},
		{MapNormal, "Q", "keys.:g", "未知の操作の ID"},
		{MapNormal, "Q", "insert.left", "normal モードでは使えない"},
		{MapInsert, "<C-a>", "line-start", "insert モードでは使えない"},
		{MapNormal, "Q", "inner-word", "normal モードでは使えない"},
		{MapVisual, "Q", "visual-swap-no", "未知の操作の ID"},
		{MapInsert, "jk", "insert.left", "1 つのキー"},
		{MapInsert, "a", "insert.left", "操作を割り当てられない"},
		{MapInsert, "<Esc>", "insert.left", "操作を割り当てられない"},
		{MapNormal, "1", "left", "先に読まれる"},
		{MapOperatorPending, "Q", "left", "割り当てられないモード"},
	} {
		if _, err := UserBinding(tt.mode, k(tt.keys), tt.id); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("UserBinding(%s, %s, %s): err = %v, want %q", ModeName(tt.mode), tt.keys, tt.id, err, tt.want)
		}
	}
}

// 操作を割り当てた登録は、同じモードの同じキーの列の既定の登録に勝つ。他のモードの既定の登録は残る。
//
// req: FR-126
func TestOverride(t *testing.T) {
	km := DefaultKeymap()
	k := editcmd.MustParseKeys
	b, err := UserBinding(MapNormal, k("w"), "word-end")
	if err != nil {
		t.Fatal(err)
	}
	if err = km.Override(b); err != nil {
		t.Fatal(err)
	}
	got := feed(NewParser(km), "w")
	if len(got) != 1 || got[0].Command.Target.Ref.ID != "word-end" {
		t.Errorf("上書きした w = %+v", got)
	}
	// オペレーターの後とビジュアルモードの w は、既定のまま残る。
	if got := feed(NewParser(km), "dw"); len(got) != 1 || got[0].Command.Target.Ref.ID != "word-next" {
		t.Errorf("dw = %+v", got)
	}
	if vb, ok := km.Lookup(MapVisual, k("w")); !ok || vb.Ref.ID != "word-next" || vb.User {
		t.Errorf("ビジュアルモードの w = %+v, %v", vb, ok)
	}
	ov := km.Overridden()
	if len(ov) != 1 || ov[0].Ref.ID != "word-next" || ov[0].Modes != MapNormal {
		t.Errorf("Overridden = %+v", ov)
	}

	// インサートモードの <BS> を別の操作にする。
	b, err = UserBinding(MapInsert, k("<BS>"), "insert.delete-char")
	if err != nil {
		t.Fatal(err)
	}
	if err = km.Override(b); err != nil {
		t.Fatal(err)
	}
	if ref, ok := NewParser(km).InsertAction(editcmd.Special(editcmd.CodeBackspace)); !ok || ref.ID != "insert.delete-char" {
		t.Errorf("InsertAction(<BS>) = %+v, %v", ref, ok)
	}

	// 規則に反する登録は、既定の登録を取り除かない。
	if km.Override(Binding{Kind: BindMotion, Modes: MapNormal, Keys: k("1"), Ref: builtin("left")}) == nil {
		t.Error("Override(1) = nil")
	}
	if km.Override(Binding{Kind: BindAction, Modes: MapInsert, Keys: k("<Left>x"), Ref: builtin("insert.right")}) == nil {
		t.Error("Override(<Left>x) = nil")
	}
	if _, ok := km.Lookup(MapInsert, k("<Left>")); !ok {
		t.Error("誤りの Override が <Left> を取り除いた")
	}
}

// インサートモードの既定のキーは、#257 の表のとおりの操作を引く。文字、<CR>、<Tab>、<Esc> と、割り当ての無い修飾キーは引かない。
//
// req: FR-127
func TestInsertAction(t *testing.T) {
	p := NewParser(DefaultKeymap())
	for keys, id := range map[string]string{
		"<Left>": "insert.left", "<Right>": "insert.right", "<Up>": "insert.up", "<Down>": "insert.down",
		"<Home>": "insert.line-start", "<End>": "insert.line-end", "<PageUp>": "insert.page-up", "<PageDown>": "insert.page-down",
		"<BS>": "insert.delete-prev-char", "<Del>": "insert.delete-char",
	} {
		ref, ok := p.InsertAction(editcmd.MustParseKeys(keys)[0])
		if !ok || ref != builtin(id) {
			t.Errorf("InsertAction(%s) = %+v, %v, want %s", keys, ref, ok, id)
		}
	}
	for _, keys := range []string{"a", "<CR>", "<Tab>", "<Esc>", "<C-a>", "<C-k>", "<S-Left>", "<Insert>"} {
		k := editcmd.MustParseKeys(keys)[0]
		if ref, ok := p.InsertAction(k); ok {
			t.Errorf("InsertAction(%s) = %+v", keys, ref)
		}
	}
	for _, keys := range []string{"a", "<CR>", "<Tab>", "<Esc>"} {
		if InsertTableKey(editcmd.MustParseKeys(keys)[0]) {
			t.Errorf("InsertTableKey(%s) = true", keys)
		}
	}
	// 既定のキーの無い insert.delete-to-line-end も、ID の一覧にある。
	found := false
	for _, a := range Actions() {
		if a.ID == "insert.delete-to-line-end" {
			found = a.Modes == MapInsert && a.Kind == BindAction
		}
	}
	if !found {
		t.Error("Actions に insert.delete-to-line-end が無い")
	}
}

// 操作の ID は一意である。同じ ID の行は、同じ操作を複数のキーに載せたもの（0 と <Home> の line-start、d とビジュアルモードの x の delete）
// だけで、種類、文字の引数、実行の後のモード、提供者へ振り分けるかが一致する。本体の操作と提供者へ振り分ける操作の ID も重ならない。
//
// req: FR-126
func TestActionIDUnique(t *testing.T) {
	type attr struct {
		kind                  BindingKind
		takesChar, takesChar2 bool
		next                  editcmd.Mode
		provider              bool
	}
	seen := map[string]attr{}
	check := func(id string, a attr) {
		if old, ok := seen[id]; ok && old != a {
			t.Errorf("ID %s の行が別の操作を指している: %+v と %+v", id, old, a)
		}
		seen[id] = a
	}
	for _, tb := range builtinTables {
		for _, s := range tb.specs {
			check(s.id, attr{kind: tb.kind, takesChar: s.takesChar, takesChar2: s.takesChar2, next: s.next})
		}
	}
	for _, s := range providerKeys {
		check(s.id, attr{kind: s.kind, provider: true})
	}

	// 複数の行（キー）を持ってよい ID と、そのキーの全部。同じ種類の別の操作が同じ ID を使うと
	// （P の ID を put-after に、インサートモードの <Del> の ID を delete-char にするなど）、ここに無い ID が 2 行を持つので落ちる
	// （PR #258 の再レビューの推奨 1）。同じ操作を別のキーにも載せるときは、ここに足す。
	multi := map[string][]string{
		"left": {"h", "<Left>"}, "down": {"j", "<Down>"}, "up": {"k", "<Up>"}, "right": {"l", "<Right>"},
		"line-start": {"0", "<Home>"}, "line-end": {"$", "<End>"},
		"delete": {"d", "x"}, "change": {"c", "s"}, "toggle-case": {"g~", "~"}, "lowercase": {"gu", "u"}, "uppercase": {"gU", "U"},
		"change-lines": {"C", "S"},
		"inner-paren":  {"i(", "i)", "ib"}, "a-paren": {"a(", "a)", "ab"}, "inner-bracket": {"i[", "i]"}, "a-bracket": {"a[", "a]"},
		"inner-brace": {"i{", "i}", "iB"}, "a-brace": {"a{", "a}", "aB"},
		"hover": {"<Leader>lh", "K"}, "definition": {"<Leader>lD", "<C-]>"},
	}
	rows := map[string][]string{}
	for _, tb := range builtinTables {
		for _, s := range tb.specs {
			rows[s.id] = append(rows[s.id], s.keys)
		}
	}
	for _, s := range providerKeys {
		rows[s.id] = append(rows[s.id], s.keys)
	}
	for id, keys := range rows {
		want, ok := multi[id]
		switch {
		case ok && !slices.Equal(keys, want):
			t.Errorf("ID %s の行のキー = %q, want %q", id, keys, want)
		case !ok && len(keys) > 1:
			t.Errorf("ID %s が %d 行（%q）にある。同じ操作を別のキーにも載せるなら multi に足す", id, len(keys), keys)
		}
	}
	for id := range multi {
		if _, ok := rows[id]; !ok {
			t.Errorf("multi の ID %s が表に無い", id)
		}
	}

	ids := map[string]bool{}
	for _, a := range Actions() {
		if ids[a.ID] {
			t.Errorf("Actions に ID %s が 2 件ある", a.ID)
		}
		ids[a.ID] = true
	}
}

// 操作の ID の一覧（docs/usage.md の「操作の ID の一覧」）が、Actions と同じである。
// ずれたら、失敗の文言に出す表で、docs/usage.md の印の間を置き換える。
//
// req: FR-126
func TestActionIDDoc(t *testing.T) {
	data, err := os.ReadFile("../../docs/usage.md")
	if err != nil {
		t.Fatal(err)
	}
	const begin, end = "<!-- action-ids:begin -->\n", "<!-- action-ids:end -->"
	_, rest, ok := strings.Cut(string(data), begin)
	if !ok {
		t.Fatalf("docs/usage.md に %q が無い", begin)
	}
	got, _, ok := strings.Cut(rest, end)
	if !ok {
		t.Fatalf("docs/usage.md に %q が無い", end)
	}
	if want := actionIDTable(); got != want {
		t.Errorf("docs/usage.md の操作の ID の一覧が操作の表とずれている。印の間を次の表に置き換える:\n%s", want)
	}
	seen := map[string]bool{}
	for _, a := range Actions() {
		if a.Description == "" || a.Modes == 0 {
			t.Errorf("%s: %+v", a.ID, a)
		}
		if seen[a.ID+a.Kind.String()] {
			t.Errorf("%s (%v) が 2 件ある", a.ID, a.Kind)
		}
		seen[a.ID+a.Kind.String()] = true
	}
}

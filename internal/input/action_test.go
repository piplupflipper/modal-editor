package input

import (
	"os"
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
		// 同じ ID が複数の種類にあれば、そのモードで使える最初の種類（オペレーターの g~）を選ぶ。
		{MapNormal, "Q", "toggle-case", BindOperator, ""},
		{MapNormal, "Q", "hover", BindAction, ProviderExtension},
		{MapVisual, "Q", "format", BindOperator, ProviderExtension},
		{MapInsert, "<C-a>", "insert.line-start", BindAction, ""},
		{MapInsert, "<C-k>", "insert.delete-to-line-end", BindAction, ""},
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

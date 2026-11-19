package input

import (
	"strings"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/editcmd"
)

// 本体の既定のキーは、どれもキーの一覧に出す説明文を持つ（PR #189 と PR #209 の未確認事項の sa、sd、sr、<、>、LSP と DAP の操作を含む）。
// <leader>l? と <leader>d? は、leader で始まる接頭辞のキーの一覧の操作として登録する。
//
// req: FR-90
func TestDescriptions(t *testing.T) {
	for _, b := range DefaultKeymap().Bindings() {
		if b.Description == "" {
			t.Errorf("%s (%s) has no description", editcmd.KeysString(b.Keys), b.Ref.ID)
		}
	}
	for _, k := range ParserKeys() {
		if k.Description == "" || k.Modes == 0 {
			t.Errorf("parser key %s: %+v", k.Keys, k)
		}
	}

	km, errs := DefaultKeymapLeader(editcmd.MustParseKeys(","))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for keys, want := range map[string][2]string{",l?": {KeyGroupLSP, ",l"}, ",d?": {KeyGroupDAP, ",d"}} {
		b, ok := km.Lookup(MapNormal, editcmd.MustParseKeys(keys))
		if !ok || !b.Ref.Provider() {
			t.Errorf("Lookup(%s) = %+v, %v", keys, b, ok)
			continue
		}
		g, prefix, ok := ParseKeyListID(b.Ref.ID)
		if !ok || g != want[0] || editcmd.KeysString(prefix) != want[1] || ProviderGroup(b.Ref.ID) != want[0] {
			t.Errorf("ParseKeyListID(%q) = %q, %q, %v", b.Ref.ID, g, editcmd.KeysString(prefix), ok)
		}
	}
	for id, want := range map[string]string{"hover": KeyGroupLSP, "diagnostic.next": KeyGroupLSP, "format": KeyGroupLSP, "debug.stepIn": KeyGroupDAP, "breakpoint.toggle": KeyGroupDAP, "nope": ""} {
		if got := ProviderGroup(id); got != want {
			t.Errorf("ProviderGroup(%q) = %q, want %q", id, got, want)
		}
	}
	for _, id := range ProviderOperations() {
		if strings.HasPrefix(id, KeyListPrefix) {
			t.Errorf("ProviderOperations has the key list %s", id)
		}
	}
}

// Bindings は登録の写しを返し、返した値を変えても Keymap は変わらない。
func TestBindingsCopy(t *testing.T) {
	km := NewKeymap()
	if err := km.Bind(Binding{Kind: BindAction, Keys: editcmd.MustParseKeys("ab"), Ref: editcmd.Ref{ID: "x"}, Description: "d"}); err != nil {
		t.Fatal(err)
	}
	bs := km.Bindings()
	bs[0].Keys[0] = editcmd.Rune('z')
	if _, ok := km.Lookup(MapNormal, editcmd.MustParseKeys("ab")); !ok {
		t.Error("changing Bindings changed the keymap")
	}
	if bs[0].Description != "d" {
		t.Errorf("Description = %q", bs[0].Description)
	}
}

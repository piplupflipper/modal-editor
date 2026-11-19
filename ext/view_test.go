package ext

import (
	"strings"
	"testing"
)

// 部品は、種類、項目の ID の空と重複、改行、色を付ける範囲を検査する。popup/open の置き場所も検査する。
//
// req: FR-72
func TestViewComponentValidate(t *testing.T) {
	item := func(id, label string, children ...ViewItem) ViewItem {
		return ViewItem{ID: id, Label: label, Children: children}
	}
	tests := []struct {
		name string
		p    PopupOpenParams
		want string // 誤りの文の一部。空なら誤りにしない
	}{
		{"テキスト", PopupOpenParams{Component: ViewComponent{Kind: ViewText, Text: "a\nb"}}, ""},
		{"一覧", PopupOpenParams{Placement: PopupCenter, Component: ViewComponent{Kind: ViewList, Items: []ViewItem{item("a", "A"), item("b", "B")}}}, ""},
		{"木", PopupOpenParams{Component: ViewComponent{Kind: ViewTree, Items: []ViewItem{item("d", "D", item("d/a", "A"))}}}, ""},
		{"入力の行", PopupOpenParams{Component: ViewComponent{Kind: ViewInput, Text: "x"}}, ""},
		{"知らない種類", PopupOpenParams{Component: ViewComponent{Kind: "grid"}}, "知らない部品"},
		{"知らない置き場所", PopupOpenParams{Placement: "side", Component: ViewComponent{Kind: ViewText}}, "知らない置き場所"},
		{"入力の行の改行", PopupOpenParams{Component: ViewComponent{Kind: ViewInput, Text: "a\nb"}}, "改行"},
		{"空の ID", PopupOpenParams{Component: ViewComponent{Kind: ViewList, Items: []ViewItem{item("", "A")}}}, "id が空"},
		{"木の中の重複", PopupOpenParams{Component: ViewComponent{Kind: ViewTree, Items: []ViewItem{item("d", "D", item("d", "A"))}}}, "重複"},
		{"一覧の子", PopupOpenParams{Component: ViewComponent{Kind: ViewList, Items: []ViewItem{item("d", "D", item("a", "A"))}}}, "tree でだけ"},
		{"表示の文字列の改行", PopupOpenParams{Component: ViewComponent{Kind: ViewList, Items: []ViewItem{item("a", "A\nB")}}}, "改行"},
		{"範囲の外", PopupOpenParams{Component: ViewComponent{Kind: ViewList, Items: []ViewItem{
			{ID: "a", Label: "abc", Highlights: []ViewSpan{{Start: 1, End: 4, Style: "keyword"}}},
		}}}, "label の外"},
	}
	for _, tt := range tests {
		err := tt.p.Validate()
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: %v", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

// ポップアップのメッセージの JSON の形（基本設計 5.3）。
func TestViewMessageJSON(t *testing.T) {
	roundTrip(t, PopupOpenParams{Placement: PopupCenter, Focus: true, Component: ViewComponent{
		Kind: ViewList, Title: "t", Items: []ViewItem{{ID: "a", Label: "A", Highlights: []ViewSpan{{Start: 0, End: 1, Style: "keyword"}}}},
	}}, `{"component":{"kind":"list","title":"t","items":[{"id":"a","label":"A","highlights":[{"start":0,"end":1,"style":"keyword"}]}]},"placement":"center","focus":true}`)
	roundTrip(t, PopupOpenResult{View: 3}, `{"view":3}`)
	roundTrip(t, ViewSelectParams{View: 3, Item: "a"}, `{"view":3,"item":"a"}`)
	roundTrip(t, ViewKeyParams{View: 3, ID: "k"}, `{"view":3,"id":"k"}`)
	roundTrip(t, InputChangeParams{View: 3, Text: "x"}, `{"view":3,"text":"x"}`)
	roundTrip(t, EditorOpenParams{Path: "/a", Position: &Position{Line: 1}, Peek: true}, `{"path":"/a","position":{"line":1,"byte":0},"peek":true}`)
}

// contributes.popupKeys は、ID とキーが空でなく、ID が popupKeys の中で重複しないことを検査する（FR-69）。
func TestManifestPopupKeys(t *testing.T) {
	base := Manifest{ID: "x", Command: "./x", Provides: []Operation{OpHover}}
	ok := base
	ok.Contributes.PopupKeys = []Contribution{{ID: "a", Keys: "o"}, {ID: "b", Keys: "<C-x>"}}
	if _, err := ok.Validate(); err != nil {
		t.Errorf("valid popupKeys: %v", err)
	}
	for _, tt := range []struct {
		keys []Contribution
		want string
	}{
		{[]Contribution{{ID: "", Keys: "o"}}, "id が空"},
		{[]Contribution{{ID: "a", Keys: ""}}, "keys が空"},
		{[]Contribution{{ID: "a", Keys: "o"}, {ID: "a", Keys: "p"}}, "重複"},
	} {
		m := base
		m.Contributes.PopupKeys = tt.keys
		if _, err := m.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("popupKeys %+v: err = %v, want %q", tt.keys, err, tt.want)
		}
	}
}

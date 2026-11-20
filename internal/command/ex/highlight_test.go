package ex

import (
	"testing"

	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// :search.clearHighlight と、その別名の :noh[lsearch] は、一致の色を消した状態にする。設定の値と直前のパターンは変えない。
// 範囲、"!"、引数は取らない（2026-11-20 に Vim 9.1 で、:3noh が E481、:noh! が E477、:noh x が E488 になり、
// :noh|let g:x = 1 が続くコマンドも実行することを確かめた）。
//
// req: FR-138
func TestClearHighlight(t *testing.T) {
	tests := []struct {
		cmdline  string
		wantCode string
	}{
		{"search.clearHighlight", ""},
		{"nohlsearch", ""},
		{"noh", ""},
		{"nohl", ""},
		{"noh|set number", ""},
		{"search.clear", "E492"},
		{"nohlsearchx", "E492"},
		{"noh x", "E488"},
		{"3noh", "E481"},
		{"noh!", "E477"},
		{"search.clearHighlight x", "E488"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			opts := &fakeOptions{}
			st := &State{LastPattern: "foo", Options: opts}
			_, err := Execute(buffer.New("a\nb\nc\n"), st, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if cleared := st.ClearedHighlight != nil; cleared != (tt.wantCode == "") {
				t.Errorf("ClearedHighlight = %+v", st.ClearedHighlight)
			}
			if st.LastPattern != "foo" {
				t.Errorf("LastPattern = %q", st.LastPattern)
			}
			if got := st.HighlightPattern(0); (got == "") != (tt.wantCode == "") {
				t.Errorf("HighlightPattern = %q", got)
			}
			for _, s := range opts.sets {
				if s != "lineNumber.show=true" {
					t.Errorf("sets = %q（search.highlight を変えない）", opts.sets)
				}
			}
		})
	}
	// 設定の値を読み書きする口が無くても消せる（回数は 0 として覚える）。
	st := &State{LastPattern: "foo"}
	if _, err := Execute(buffer.New("a\n"), st, 0, "noh"); err != nil || st.HighlightPattern(0) != "" {
		t.Errorf("口の無い :noh = %v, %q", err, st.HighlightPattern(0))
	}
}

// 消した状態は、パターンを使う Ex コマンド（:s、:g、:v、パターンの範囲）で解く。パターンが見つからなくても解く。
// search.highlight を設定し直したとき（同じ値でも）にも効かなくなるが、ほかの設定と、値を読むだけの :set hlsearch? では効いたままである。
// 期待値は、2026-11-20 に Vim 9.1 で :nohlsearch の後に同じコマンドを打ち、v:hlsearch を読んで確かめた（:s、:g、:/bar/、
// 見つからない :s/qqq/ と :g/qqq/、:set hlsearch、:set hlsearch!、:set number、:set hlsearch?）。
//
// req: FR-138
func TestClearHighlightRestore(t *testing.T) {
	tests := []struct {
		cmdline string
		shown   bool   // 実行した後に色を付けるか
		pattern string // 実行した後の直前のパターン
	}{
		{"s/a/x/", true, "a"},
		{"s/qqq/x/", true, "qqq"},
		{"s//x/", true, "foo"},
		{"g/b/", true, "b"},
		{"g/qqq/", true, "qqq"},
		{"v/b/d", true, "b"},
		{"/c/", true, "c"},
		{"s/(/x/", false, "foo"},
		{"set hlsearch", true, "foo"},
		{"set nohlsearch", true, "foo"},
		{"set hlsearch!", true, "foo"},
		{"option.set search.highlight true", true, "foo"},
		{"set hlsearch?", false, "foo"},
		{"option.get search.highlight", false, "foo"},
		{"set number", false, "foo"},
		{"2", false, "foo"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			opts := &fakeOptions{vals: map[string]option.Value{option.SearchHighlight: option.BoolValue(true)}}
			d, _ := option.Lookup(option.SearchHighlight)
			st := &State{LastPattern: "foo", Options: opts}
			b := buffer.New("foo a\nb\nc\n")
			if _, err := Execute(b, st, 0, "noh"); err != nil {
				t.Fatal(err)
			}
			if got := st.HighlightPattern(opts.Revision(d)); got != "" {
				t.Fatalf(":noh の後の HighlightPattern = %q", got)
			}
			_, _ = Execute(b, st, 0, tt.cmdline) // 見つからない :s と :g は誤りを返す
			got := st.HighlightPattern(opts.Revision(d))
			if (got != "") != tt.shown {
				t.Errorf("HighlightPattern = %q, want 色を付ける=%v", got, tt.shown)
			}
			if st.LastPattern != tt.pattern {
				t.Errorf("LastPattern = %q, want %q", st.LastPattern, tt.pattern)
			}
		})
	}
}

// :set の Vim の名前 hlsearch は search.highlight の別名で、真偽の書き方（hlsearch、nohlsearch、hlsearch!、hlsearch?）を受け付ける。
//
// req: FR-137
func TestHighlightOption(t *testing.T) {
	tests := []struct {
		cmdline    string
		init       bool
		wantSets   []string
		wantNotice string
		wantCode   string
	}{
		{cmdline: "set hlsearch", wantSets: []string{"search.highlight=true"}},
		{cmdline: "set nohlsearch", init: true, wantSets: []string{"search.highlight=false"}},
		{cmdline: "set hlsearch!", wantSets: []string{"search.highlight=true"}},
		{cmdline: "set hlsearch!", init: true, wantSets: []string{"search.highlight=false"}},
		{cmdline: "set hlsearch?", init: true, wantNotice: "search.highlight=true"},
		{cmdline: "set hlsearch=1", wantCode: "E474"},
		{cmdline: "set hls", wantCode: "E518"},
	}
	for _, tt := range tests {
		t.Run(tt.cmdline, func(t *testing.T) {
			opts := &fakeOptions{vals: map[string]option.Value{option.SearchHighlight: option.BoolValue(tt.init)}}
			res, err := Execute(buffer.New("a\n"), &State{Options: opts}, 0, tt.cmdline)
			if code := errCode(t, err); code != tt.wantCode {
				t.Fatalf("code = %q, want %q (err = %v)", code, tt.wantCode, err)
			}
			if len(opts.sets) != len(tt.wantSets) || (len(tt.wantSets) > 0 && opts.sets[0] != tt.wantSets[0]) {
				t.Errorf("sets = %q, want %q", opts.sets, tt.wantSets)
			}
			if tt.wantCode == "" && res.Notice.Text != tt.wantNotice {
				t.Errorf("notice = %q, want %q", res.Notice.Text, tt.wantNotice)
			}
		})
	}
}

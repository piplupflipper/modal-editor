package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piplupflipper/vi-imitation/ext"
)

// progressString は、progress/report の params を、試験で比べる 1 つの文字列にする。
func progressString(p ext.ProgressReportParams) string {
	s := string(p.Kind) + "|" + p.Title + "|" + p.Message
	if p.Percentage != nil {
		s += fmt.Sprintf("|%d", *p.Percentage)
	}
	return s
}

// TestWorkDoneProgress は、initialize で window.workDoneProgress を申し出て、window/workDoneProgress/create に答え、
// 言語サーバーの $/progress の begin、report、end を本体の progress/report に写すことを確かめる。
// 始めていない識別子の report と、部分の結果の $/progress は捨てる。言語サーバーが終わったら、終わっていない進み具合を end で終える。
//
// req: FR-179
func TestWorkDoneProgress(t *testing.T) {
	h := startHost(t, hostOptions{args: []string{"-progress"}, maxRestarts: 1})
	h.open(1, "a.go", "a")
	name := filepath.Base(fakeLS)
	var got []string
	var tokens []string
	for len(got) < 4 {
		p := receive(t, h.prog, "progress/report")
		got = append(got, progressString(p))
		tokens = append(tokens, p.Token)
	}
	want := []string{
		"begin|" + name + ": Loading||0",
		"report||1/2|50",
		"end||done",
		"begin|" + name + ": Indexing|",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("progress =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if tokens[0] != tokens[1] || tokens[1] != tokens[2] || tokens[2] == tokens[3] || !strings.HasPrefix(tokens[0], "lsp/go/") {
		t.Errorf("tokens = %q", tokens)
	}
	// fakels は、window/workDoneProgress/create が誤りで答えられたら $/progress を送らないので、begin が届いたことで答えたと分かる。

	// 言語サーバーが異常終了したら、終わっていない識別子 7 の進み具合を終える。
	h.change(1, edit(0, 0, 0, 0, "CRASH "))
	p := receive(t, h.prog, "progress/report after crash")
	if p.Kind != ext.ProgressEnd || p.Token != tokens[3] {
		t.Errorf("after crash = %+v, want end of %s", p, tokens[3])
	}
	// 再起動した言語サーバーの識別子は、前のものと重ならない。
	if p := receive(t, h.prog, "progress/report after restart"); p.Kind != ext.ProgressBegin || p.Token == tokens[0] {
		t.Errorf("after restart = %+v, token before %s", p, tokens[0])
	}
}

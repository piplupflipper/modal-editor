package editor

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
)

// startProgress は、40 列 12 行の端末でエディタを起動し、タイマーを偽物に差し替える。
func startProgress(t *testing.T, opts map[string]option.Value) (*harness, *fakeTimers) {
	t.Helper()
	dir := writeFiles(t, map[string]string{"a.txt": "abc\n"})
	term := newFakeTerm()
	term.w, term.h = 40, 12
	h := startTerm(t, term, Config{WorkDir: dir, Options: opts}, "a.txt")
	ft := &fakeTimers{}
	h.e.do(func() { h.e.afterFunc = ft.after })
	h.settle()
	return h, ft
}

// progressRows は、画面のバッファの表示領域（タブの行の下からステータス行の上まで）の各行の、右の空白を除いた文字列を返す。
func progressRows(h *harness) []string {
	lines := strings.Split(h.term.screen(), "\n")
	if len(lines) < 3 {
		return nil
	}
	rows := lines[1 : len(lines)-2]
	for i, l := range rows {
		rows[i] = strings.TrimRight(l, " ")
	}
	return rows
}

// waitProgress は、表示領域の最後の行から上へ、want の行が並ぶ（want の最後が最後の行）まで待つ。
// want が空なら、どの行も進み具合を出していない（"~" か本文だけの行）まで待つ。
func waitProgress(h *harness, want ...string) {
	h.t.Helper()
	h.eventually(fmt.Sprintf("進み具合 %q", want), func() bool {
		rows := progressRows(h)
		if len(want) == 0 {
			return !slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, "...") || strings.Contains(r, "done") })
		}
		if len(rows) < len(want) {
			return false
		}
		tail := rows[len(rows)-len(want):]
		for i, w := range want {
			if !strings.HasSuffix(tail[i], w) {
				return false
			}
		}
		return true
	})
}

func begin(token, title, msg string, pct *int) ext.ProgressReportParams {
	return ext.ProgressReportParams{Token: token, Kind: ext.ProgressBegin, Title: title, Message: msg, Percentage: pct}
}

// 拡張機能が progress/report で知らせた進み具合を、client の画面の右下に、始めた順に積んで出す。
// report は本文と百分率のうち書いたものだけを変える。end は終わった印を付け、progress.doneMs の後に消す。
// 始めていない識別子の report と end は捨て、識別子は拡張機能ごとに分ける。拡張機能が異常で終わったら、その拡張機能の進み具合を消す。
//
// req: FR-178
func TestProgressDisplay(t *testing.T) {
	h, ft := startProgress(t, nil)
	report := func(extID string, p ext.ProgressReportParams) {
		h.e.do(func() { h.e.reportProgress(extID, p) })
	}

	report("lsp", begin("1", "gopls", "load", ext.Percent(10)))
	waitProgress(h, " load (10%) gopls ...")
	report("lsp", ext.ProgressReportParams{Token: "1", Kind: ext.ProgressReport, Percentage: ext.Percent(150)})
	waitProgress(h, " load (100%) gopls ...")
	report("lsp", ext.ProgressReportParams{Token: "1", Kind: ext.ProgressReport, Message: "3/4\nfiles"})
	waitProgress(h, " 3/4 files (100%) gopls ...")
	// 同じ識別子でも、他の拡張機能の進み具合とは分ける。
	report("ts", begin("1", "parser", "", nil))
	waitProgress(h, " 3/4 files (100%) gopls ...", " parser ...")
	// 始めていない識別子の report と end は捨てる。
	report("lsp", ext.ProgressReportParams{Token: "x", Kind: ext.ProgressReport, Message: "no"})
	report("lsp", ext.ProgressReportParams{Token: "x", Kind: ext.ProgressEnd})

	report("lsp", ext.ProgressReportParams{Token: "1", Kind: ext.ProgressEnd, Message: "ok"})
	waitProgress(h, " ok gopls done", " parser ...")
	if n := ft.count(); n != 1 || ft.ds[0] != 3000*time.Millisecond {
		t.Fatalf("タイマー = %d 個 %v, want 1 個の 3s", n, ft.ds)
	}
	// 終えた後の report は捨てる。
	report("lsp", ext.ProgressReportParams{Token: "1", Kind: ext.ProgressReport, Message: "late"})
	ft.fire(0)
	waitProgress(h, " parser ...")
	if rows := progressRows(h); slices.ContainsFunc(rows, func(r string) bool { return strings.Contains(r, "gopls") }) {
		t.Errorf("終えた進み具合が時間の後も残る: %q", rows)
	}

	// 出さない設定の間は描かないが、持っておき、出す設定に戻せば出る。
	setShow := func(v bool) {
		h.e.do(func() {
			d, _ := option.Lookup(option.ProgressShow)
			if err := h.e.options.Set(d, option.BoolValue(v)); err != nil {
				t.Fatal(err)
			}
		})
	}
	setShow(false)
	h.keys("l") // 描き直させる
	waitProgress(h)
	setShow(true)
	h.keys("h")
	waitProgress(h, " parser ...")

	// 拡張機能が異常で終わったら、その拡張機能の進み具合を消す。終えて時間を待っているもののタイマーも止める。
	report("lsp", begin("2", "gopls", "", nil))
	report("lsp", ext.ProgressReportParams{Token: "2", Kind: ext.ProgressEnd})
	waitProgress(h, " parser ...", " gopls done")
	h.e.do(func() {
		h.e.stateChange(&exthost.StateChange{Extension: "ts", State: exthost.StateRestartWait})
		h.e.stateChange(&exthost.StateChange{Extension: "lsp", State: exthost.StateDisabled})
	})
	waitProgress(h)
	ft.mu.Lock()
	stopped := ft.stopped[1]
	ft.mu.Unlock()
	if !stopped {
		t.Error("止まった拡張機能の進み具合のタイマーを止めていない")
	}
}

// 同じ識別子の begin は、続いている進み具合を始め直す。progress.doneMs が 0 なら、終えたらすぐに消す。
// 1 つの拡張機能の進み具合は maxProgressPerExtension までで、超えたら古いものを捨てる。
//
// req: FR-178
func TestProgressRestartAndLimit(t *testing.T) {
	h, ft := startProgress(t, map[string]option.Value{option.ProgressDoneMs: option.IntValue(0)})
	var got []string
	h.e.do(func() {
		e := h.e
		e.reportProgress("lsp", begin("a", "first", "m", ext.Percent(40)))
		e.reportProgress("lsp", begin("b", "second", "", nil))
		e.reportProgress("lsp", begin("a", "again", "", nil))
		for _, it := range e.progress {
			got = append(got, fmt.Sprintf("%s:%s:%s:%d", it.token, it.ui.Title, it.ui.Message, it.ui.Percentage))
		}
		e.reportProgress("lsp", ext.ProgressReportParams{Token: "a", Kind: ext.ProgressEnd})
		got = append(got, fmt.Sprint(len(e.progress)))
		for i := range maxProgressPerExtension + 2 {
			e.reportProgress("many", begin(fmt.Sprint(i), "t", "", nil))
		}
		var tokens []string
		for _, it := range e.progress {
			if it.ext == "many" {
				tokens = append(tokens, it.token)
			}
		}
		got = append(got, fmt.Sprint(len(tokens)), tokens[0])
	})
	want := []string{"a:again::-1", "b:second::-1", "1", fmt.Sprint(maxProgressPerExtension), "2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if ft.count() != 0 {
		t.Errorf("progress.doneMs が 0 なのにタイマーを掛けた")
	}
}

// 入れ子の待ちの間に届いた進み具合の report は、同じ識別子の前の report と、省いた本文と百分率を引き継いでまとめ、
// 上限を超えたら捨ててよい。begin と end は捨てない。begin と end をまたいではまとめない。
//
// req: FR-178
func TestHoldProgress(t *testing.T) {
	h := start(t, Config{Host: exthost.Config{MaxQueuedEvents: 4}})
	h.e.do(func() {
		e := h.e
		pr := func(p ext.ProgressReportParams) *exthost.ProgressReport {
			return &exthost.ProgressReport{Extension: "lsp", Params: p}
		}
		e.hold(pr(begin("t", "gopls", "", nil)))
		e.hold(pr(ext.ProgressReportParams{Token: "t", Kind: ext.ProgressReport, Message: "a", Percentage: ext.Percent(1)}))
		e.hold(pr(ext.ProgressReportParams{Token: "t", Kind: ext.ProgressReport, Percentage: ext.Percent(2)}))
		e.hold(pr(ext.ProgressReportParams{Token: "t", Kind: ext.ProgressEnd}))
		e.hold(pr(ext.ProgressReportParams{Token: "t", Kind: ext.ProgressReport, Message: "after end"}))
		var got []string
		for _, ev := range e.held {
			p := ev.(*exthost.ProgressReport).Params
			s := string(p.Kind) + ":" + p.Message
			if p.Percentage != nil {
				s += fmt.Sprint(":", *p.Percentage)
			}
			got = append(got, s)
		}
		if want := []string{"begin:", "report:a:2", "end:", "report:after end"}; !slices.Equal(got, want) {
			t.Errorf("held = %q, want %q", got, want)
		}
		// 上限を超えたら、report を捨て、begin と end は残す。
		for range 3 {
			e.hold(pr(begin("u", "x", "", nil)))
		}
		got = got[:0]
		for _, ev := range e.held {
			got = append(got, string(ev.(*exthost.ProgressReport).Params.Kind))
		}
		if want := []string{"begin", "end", "begin", "begin", "begin"}; !slices.Equal(got, want) {
			t.Errorf("上限を超えた後の held = %q, want %q", got, want)
		}
		e.held = nil
	})
}

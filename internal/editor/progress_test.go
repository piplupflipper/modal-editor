package editor

import (
	"fmt"
	"os"
	"path/filepath"
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
			return !slices.ContainsFunc(rows, func(r string) bool {
				return strings.Contains(r, "...") || strings.Contains(r, "done") || strings.Contains(r, "failed")
			})
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
	// 失敗して終えたものは、失敗の印で出す（PR #345 のレビューの推奨 2）。
	report("lsp", ext.ProgressReportParams{Token: "2", Kind: ext.ProgressEnd, Failed: true})
	waitProgress(h, " parser ...", " gopls failed")
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

// 入れ子の待ちの間と確認の問いの間に届いた進み具合は、取っておかずにその場で並びに当てる。取っておく列には入らないので、
// begin と end を送り続けても列は増えない（PR #345 のレビューの必須 2）。
// 拡張機能が止まったことを表す状態の変化は、取っておくときにその拡張機能の進み具合を消し、後で処理するときには消し直さない
// （待ちの間に起動し直した後の進み具合を残す）。
//
// req: FR-178
func TestHoldProgress(t *testing.T) {
	h := start(t, Config{Host: exthost.Config{MaxQueuedEvents: 16}})
	h.e.do(func() {
		e := h.e
		pr := func(p ext.ProgressReportParams) *exthost.ProgressReport {
			return &exthost.ProgressReport{Extension: "lsp", Params: p}
		}
		if !e.hold(pr(begin("t", "gopls", "a", nil))) || len(e.held) != 0 || len(e.progress) != 1 || e.progress[0].ui.Message != "a" {
			t.Fatalf("その場で当てていない: held %d, progress %d", len(e.held), len(e.progress))
		}
		for i := range 10000 {
			tok := fmt.Sprint("u", i)
			e.hold(pr(begin(tok, "x", "", nil)))
			e.hold(pr(ext.ProgressReportParams{Token: tok, Kind: ext.ProgressEnd}))
		}
		if len(e.held) != 0 {
			t.Errorf("held = %d, want 0", len(e.held))
		}
		if n := len(e.progress); n > maxProgressPerExtension {
			t.Errorf("progress = %d, want <= %d", n, maxProgressPerExtension)
		}

		// 待ちの間に lsp が異常終了して起動し直し、新しい進み具合を送った。
		e.progress = nil
		e.hold(pr(begin("old", "gopls", "", nil)))
		crash := &exthost.StateChange{Extension: "lsp", State: exthost.StateRestartWait}
		if !e.hold(crash) || len(e.progress) != 0 {
			t.Errorf("止まった拡張機能の進み具合を、取っておくときに消していない: %d", len(e.progress))
		}
		e.hold(pr(begin("new", "gopls", "", nil)))
		held := e.held
		e.held = nil
		for _, ev := range held {
			e.handleEvent(ev)
		}
		if len(e.progress) != 1 || e.progress[0].token != "new" {
			t.Errorf("後で処理した状態の変化が、起動し直した後の進み具合を消した: %+v", e.progress)
		}
		// 取っておかずに処理した状態の変化は、今までどおり消す。
		e.stateChange(&exthost.StateChange{Extension: "lsp", State: exthost.StateDisabled})
		if len(e.progress) != 0 || len(e.progressCleared) != 0 {
			t.Errorf("状態の変化で消していない: %d, %d", len(e.progress), len(e.progressCleared))
		}
	})
}

// 保存の前の整形（buffer/willSave の応答の入れ子の待ち）の間に拡張機能が送った進み具合は、待ちが明ける前に画面に出る
// （PR #345 のレビューの必須 1）。待ちの間はイベントループが inspect を処理しないので、画面は端末の偽物から直に読む。
//
// req: FR-178
func TestProgressDuringWillSave(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "willSaveProgress\n"})
	term := newFakeTerm()
	term.w, term.h = 60, 12
	h := startTerm(t, term, Config{WorkDir: dir, Extensions: fakeSpecs(t), Host: exthost.Config{WillSaveTimeout: 10 * time.Second}}, "a.txt")
	h.keys(`\d`) // fake.dump で拡張機能を起動する（TestUndoPersistWriteFailsWithExtError と同じ）
	h.eventually("拡張機能の稼働", func() bool { return h.state("fake") == exthost.StateRunning })
	h.settle()
	h.keys(":w<CR>")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(h.term.screen(), "formatting fake ...") {
		if time.Now().After(deadline) {
			t.Fatalf("待ちの間に進み具合が出ない:\n%s", h.term.screen())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.eventually("終わった進み具合", func() bool { return strings.Contains(h.term.screen(), "formatted fake done") })
}

// 進み具合は、その拡張機能が後から届けるものを出す client（その拡張機能へ最後に問い合わせを始めた client。無ければ操作中の client）の
// 画面にだけ出し、分割したペインのもう 1 つの client の画面には出さない（PR #345 のレビューの推奨 1）。
//
// req: FR-178
func TestProgressClient(t *testing.T) {
	dir := writeFiles(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	term := newFakeTerm()
	term.w, term.h = 40, 12
	h := startTerm(t, term, Config{WorkDir: dir}, "a.txt")
	b := h.attach(ClientInfo{ID: 2, Files: []string{filepath.Join(dir, "b.txt")}})
	b.keys("l") // 2 つ目の client を操作中にする
	h.settle()
	h.e.do(func() { h.e.reportProgress("lsp", begin("1", "gopls", "", nil)) })
	h.eventually("操作中の client に出す", func() bool {
		return strings.Contains(b.term.screen(), "gopls ...") && !strings.Contains(h.term.screen(), "gopls ...")
	})
	// 拡張機能へ問い合わせを始めた client があれば、そちらに出す。
	h.e.do(func() { h.e.extClient["lsp"] = h.e.clients[0] })
	h.keys("l")
	b.keys("h")
	h.eventually("問い合わせを始めた client に出す", func() bool {
		return strings.Contains(h.term.screen(), "gopls ...") && !strings.Contains(b.term.screen(), "gopls ...")
	})
}

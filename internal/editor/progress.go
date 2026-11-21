package editor

import (
	"slices"
	"time"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/exthost"
	"github.com/piplupflipper/vi-imitation/internal/option"
	"github.com/piplupflipper/vi-imitation/internal/ui"
)

// 進み具合の表示（基本設計 6.7 の「進み具合の表示」。progress/report。FR-178）。
//
// 拡張機能が progress/report で知らせた進み具合を、本体の全体で 1 つの並び（Editor.progress）に、始めた順に持つ。
// 進み具合は、その拡張機能が後から届けるもの（editor/open、message/show、choice/ask）を出す client（extView。その拡張機能へ最後に
// 問い合わせを始めた client で、問い合わせていなければ操作中の client）の画面にだけ、右下に重ねる（uiProgress。描き方は ui の drawProgress）。
// 分割したペインのどの編集の client にも出すと、同じ進み具合が 2 回出るためである（PR #345 のレビューの推奨 1）。
// 出す client は描くたびに決めるので、操作中の client が替われば、進み具合もその client の画面へ移る。
// 部品の client の画面（端末多重化ソフトのペインとポップアップの中）には出さない。
//
// 入れ子の待ちの間と確認の問いの間も、進み具合は取っておかずにその場で当てて描き直す（query.go の hold）。
//
//   - begin: 拡張機能と識別子の組で 1 つの進み具合を始める。続いている同じ組があれば、それを始め直す（並びの位置は変えない）。
//     拡張機能ごとに maxProgressPerExtension を超えたら、その拡張機能の古いものから捨てる
//   - report: 続いている進み具合の本文か百分率を変える。始めていない組と、終えた組のものは捨てる
//   - end: 終わった印を付け（Failed なら失敗の印）、設定 progress.doneMs のミリ秒の後に並びから外す。0 ならすぐに外す。
//     時間の待ちはイベントループの外のタイマー（Editor.afterFunc）で行い、時間が来たら inspect でイベントループに戻して外す
//     （基本設計 2.1。一時的な色付けの flash.go と同じ）
//   - 拡張機能が異常で終わるか止まったら（状態の変化。clearProgressNow と clearProgressLater）、その拡張機能の進み具合をすべて外す
//
// 設定 progress.show が false の間も並びは持ち、描かないだけにする。false の間に始まったものも、true にすれば出る。

// maxProgressPerExtension は、1 つの拡張機能について持つ進み具合の数の上限である（fidget.nvim の render_limit の既定の 16）。
// 終えない進み具合を送り続ける拡張機能があっても、持つ数が増え続けないようにする。
const maxProgressPerExtension = 16

// maxProgressRunes は、進み具合の表題と本文として持つ文字の数の上限である（ステータス行の部品の maxStatusPartRunes と同じ）。
const maxProgressRunes = maxStatusPartRunes

// progressItem は、1 つの進み具合である。
type progressItem struct {
	ext, token string
	ui         ui.Progress
	// stop は、終えた後に外すタイマーを止める。続いている間は nil である。
	stop func() bool
}

// reportProgress は、拡張機能 extID からの progress/report を、進み具合の並びに当てる（このファイルの doc）。
//
// req: FR-178
func (e *Editor) reportProgress(extID string, p ext.ProgressReportParams) {
	i := slices.IndexFunc(e.progress, func(it *progressItem) bool { return it.ext == extID && it.token == p.Token })
	switch p.Kind {
	case ext.ProgressBegin:
		it := &progressItem{ext: extID, token: p.Token, ui: ui.Progress{
			Title: progressText(p.Title), Message: progressText(p.Message), Percentage: progressPercent(p.Percentage, -1),
		}}
		if i >= 0 {
			e.progress[i].stopTimer()
			e.progress[i] = it
			return
		}
		e.progress = append(e.progress, it)
		e.trimProgress(extID)
	case ext.ProgressReport:
		if i < 0 || e.progress[i].ui.Done {
			e.log.Debug("始めていない進み具合の report を捨てる", "extension", extID, "token", p.Token)
			return
		}
		it := e.progress[i]
		if p.Message != "" {
			it.ui.Message = progressText(p.Message)
		}
		it.ui.Percentage = progressPercent(p.Percentage, it.ui.Percentage)
	case ext.ProgressEnd:
		if i < 0 || e.progress[i].ui.Done {
			e.log.Debug("始めていない進み具合の end を捨てる", "extension", extID, "token", p.Token)
			return
		}
		it := e.progress[i]
		it.ui.Done, it.ui.Failed, it.ui.Percentage = true, p.Failed, -1
		if p.Message != "" {
			it.ui.Message = progressText(p.Message)
		}
		d := time.Duration(e.options.Get(option.ProgressDoneMs).Int()) * time.Millisecond
		if d <= 0 {
			e.removeProgress(it)
			return
		}
		stop := e.stop
		it.stop = e.afterFunc(d, func() {
			select {
			case e.inspect <- func() { e.removeProgress(it) }:
			case <-stop:
			}
		})
	}
}

// trimProgress は、拡張機能 extID の進み具合が maxProgressPerExtension を超えていれば、古いものから外す。
func (e *Editor) trimProgress(extID string) {
	n := 0
	for _, it := range e.progress {
		if it.ext == extID {
			n++
		}
	}
	for i := 0; n > maxProgressPerExtension && i < len(e.progress); {
		if it := e.progress[i]; it.ext == extID {
			it.stopTimer()
			e.progress = slices.Delete(e.progress, i, i+1)
			n--
			continue
		}
		i++
	}
}

// removeProgress は、進み具合 it を並びから外す。既に外していれば（始め直した後や、拡張機能が止まった後に、止める前のタイマーが来た）
// 何もしない。
//
// req: FR-178
func (e *Editor) removeProgress(it *progressItem) {
	e.progress = slices.DeleteFunc(e.progress, func(p *progressItem) bool { return p == it })
}

// stopsExtension は、状態の変化 st が、拡張機能が止まり、その表示のデータを消すものか（stateChange が clearExtension を呼ぶか）を返す。
func stopsExtension(st exthost.State) bool {
	switch st {
	case exthost.StateRestartWait, exthost.StateDisabled, exthost.StateNotStarted:
		return true
	case exthost.StateStarting, exthost.StateRunning, exthost.StateStopping:
	}
	return false
}

// clearProgressNow は、入れ子の待ちの間と確認の問いの間に取っておく状態の変化 ev（hold）が拡張機能の止まったことを表すなら、
// その拡張機能の進み具合をその場で外し、外したことを覚えて true を返す。進み具合は取っておかずにその場で当てる（hold の doc）ので、
// 取っておいた状態の変化を後で処理するときに外すと、待ちの間に起動し直した拡張機能の進み具合まで外れるためである
// （PR #345 のレビューの必須 1 の注意）。
//
// req: FR-178
func (e *Editor) clearProgressNow(ev *exthost.StateChange) bool {
	if !stopsExtension(ev.State) {
		return false
	}
	e.clearProgress(ev.Extension)
	if e.progressCleared == nil {
		e.progressCleared = map[*exthost.StateChange]bool{}
	}
	e.progressCleared[ev] = true
	return true
}

// clearProgressLater は、状態の変化 ev を処理するとき（stateChange）に、拡張機能が止まったことを表すなら、その拡張機能の進み具合を外す。
// 取っておいたときに外したもの（clearProgressNow）は、外し直さない。
//
// req: FR-178
func (e *Editor) clearProgressLater(ev *exthost.StateChange) {
	if e.progressCleared[ev] {
		delete(e.progressCleared, ev)
		return
	}
	if stopsExtension(ev.State) {
		e.clearProgress(ev.Extension)
	}
}

// clearProgress は、拡張機能 extID の進み具合をすべて外す。拡張機能が異常で終わったか止まったときに呼ぶ。
//
// req: FR-178
func (e *Editor) clearProgress(extID string) {
	e.progress = slices.DeleteFunc(e.progress, func(it *progressItem) bool {
		if it.ext != extID {
			return false
		}
		it.stopTimer()
		return true
	})
}

// stopTimer は、終えた後に外すタイマーがあれば止める。
func (it *progressItem) stopTimer() {
	if it.stop != nil {
		it.stop()
		it.stop = nil
	}
}

// uiProgress は、読み込んでいる client（e.view）の画面の右下に重ねる進み具合の一覧（ui.Frame.Progress）を、設定の今の値
// （progress.show）から作る。出すのは、その拡張機能が後から届けるものを出す client（extView）が、読み込んでいる client のものだけである
// （このファイルの doc）。出さないときと、出す進み具合が無いときは nil を返す。使うのは bufferFrame だけである。
//
// req: FR-178
func (e *Editor) uiProgress() []ui.Progress {
	if len(e.progress) == 0 || !e.options.Get(option.ProgressShow).Bool() {
		return nil
	}
	var out []ui.Progress
	for _, it := range e.progress {
		if e.extView(it.ext) == e.view {
			out = append(out, it.ui)
		}
	}
	return out
}

// progressText は、表題か本文を、画面の 1 行に描く文字列にする。改行と制御文字を空白 1 つに置き換え（ステータス行の部品と同じ）、
// maxProgressRunes 文字までにする。
func progressText(s string) string {
	s = ext.StatusLineText(s)
	if r := []rune(s); len(r) > maxProgressRunes {
		s = string(r[:maxProgressRunes])
	}
	return s
}

// progressPercent は、progress/report の百分率を 0 から 100 に寄せて返す。省かれていれば（nil）prev を返す。
func progressPercent(p *int, prev int) int {
	if p == nil {
		return prev
	}
	return min(max(*p, 0), 100)
}

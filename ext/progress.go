package ext

import (
	"errors"
	"fmt"
)

// MethodProgressReport は、拡張機能から本体への通知である。params は ProgressReportParams。
// 時間のかかる処理（言語サーバーの読み込み、パーサのビルド、道具の入れ込み）の進み具合を知らせる（基本設計 5.3、6.7。FR-178）。
//
// 中身は LSP 3.17 の WorkDoneProgress の begin、report、end に対応させた。1 つの処理を、拡張機能が決めた識別子（Token）で表し、
// begin で始め、report で中身を変え、end で終える。本体は、続いている進み具合と、終わってから設定 progress.doneMs のミリ秒が
// 過ぎていないものを、編集の client の画面の右下に重ねて出す（設定 progress.show が false なら出さない）。
// 本体が入れ子の待ち（保存の前の整形の buffer/willSave の応答など）をしている間も、取っておかずにその場で出す。
//
// 識別子は拡張機能ごとに分けて扱うので、他の拡張機能の識別子と重なってもよい。
// 続いている識別子の begin は、その進み具合を始め直す（表題と本文と百分率を置き換える）。
// begin の無い（終えた後を含む）識別子の report と end は捨てる。
// 拡張機能が異常で終わるか止まったら、本体はその拡張機能の進み具合をすべて消す（他の表示のデータと同じ。基本設計 6.7）。
// 拡張機能は、終えずに止まる処理（取り消し、失敗）も、Failed を true にした end で終える。失敗の理由は message/show で知らせる。
//
// 本体は、その拡張機能が後から届けるもの（editor/open、message/show、choice/ask）を出す client と同じ client の画面にだけ出す
// （その拡張機能へ最後に問い合わせを始めた client。問い合わせていなければ操作中の client）。分割したペインのどの client にも出すと、
// 同じ進み具合が 2 回出るためである。
//
// 古い本体（0.13.0 まで）は、知らない通知として捨てる（ログに残すだけ）。
const MethodProgressReport = "progress/report"

// ProgressKind は、progress/report の種類である（LSP の WorkDoneProgressBegin、WorkDoneProgressReport、WorkDoneProgressEnd の kind）。
type ProgressKind string

// 進み具合の知らせの種類。
const (
	// ProgressBegin は、進み具合を始める。Title を読む。
	ProgressBegin ProgressKind = "begin"
	// ProgressReport は、続いている進み具合の本文か百分率を変える。
	ProgressReport ProgressKind = "report"
	// ProgressEnd は、進み具合を終える。本体は終わった印を付けて、設定 progress.doneMs のミリ秒の後に消す。
	ProgressEnd ProgressKind = "end"
)

// ProgressReportParams は、progress/report の params である。
type ProgressReportParams struct {
	// Token は、拡張機能が決めた、1 つの処理を表す識別子である。空にできない。
	Token string `json:"token"`
	// Kind は、知らせの種類である。
	Kind ProgressKind `json:"kind"`
	// Title は、処理の表題（"gopls" の "Loading packages" に当たる短い語）である。begin でだけ読み、report と end では捨てる。
	Title string `json:"title,omitempty"`
	// Message は、本文（"3/10 files" など）である。report と end で空なら、前の本文のままにする（LSP と同じ）。
	Message string `json:"message,omitempty"`
	// Percentage は、0 から 100 の百分率である。省くと（nil）、begin では出さず、report では前の値のままにする。end では読まない。
	// 範囲の外の値は、本体が 0 か 100 に寄せる。
	Percentage *int `json:"percentage,omitempty"`
	// Failed は、end で、処理が成り立たずに終わった（失敗した、取り消した、処理していたプロセスが終わった）ことを表す。
	// 本体は、終わった印の代わりに失敗の印（symbols の progress.failed）と色（progress.failed）で描く。begin と report では読まない。
	Failed bool `json:"failed,omitempty"`
}

// Percent は、ProgressReportParams.Percentage に入れる値を作る。
func Percent(n int) *int {
	return &n
}

// Validate は、progress/report の params を本体が受け付けるかを確かめる。識別子が空のものと、知らない種類を断る。
// 本体は、断ったものをログに残して捨てる。
func (p ProgressReportParams) Validate() error {
	if p.Token == "" {
		return errors.New("progress/report: empty token")
	}
	switch p.Kind {
	case ProgressBegin, ProgressReport, ProgressEnd:
		return nil
	}
	return fmt.Errorf("progress/report: unknown kind %q", p.Kind)
}

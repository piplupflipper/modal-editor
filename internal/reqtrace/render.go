package reqtrace

import (
	"fmt"
	"strings"
)

// Render は、進み具合の一覧を Markdown にする。
// linkPrefix は、出力するファイルからリポジトリのルートへの相対パスである（例: "../"）。
//
// 出力に日付などの実行のたびに変わる値を含めない。
// 生成物が最新かどうかを、再生成した結果との比較で検査するためである。
func Render(r Report, linkPrefix string) string {
	var b strings.Builder
	total := len(r.Entries)

	b.WriteString("# 要件ごとの進捗\n\n")
	b.WriteString("<!-- このファイルは make progress が生成する。手で編集しない。 -->\n\n")
	b.WriteString("要件定義（[requirements.md](requirements.md)）の ID と、ソースとテストのコメントに書いた注釈を突き合わせた結果である。\n")
	b.WriteString("状態は注釈の有無だけから決まり、実装が要件を満たしているかどうかは表さない。\n")
	b.WriteString("注釈の書き方は[基本設計](basic-design.md)の「要件との対応」にある。\n\n")

	b.WriteString("## 集計\n\n")
	b.WriteString("| 状態 | 件数 | 意味 |\n|---|---|---|\n")
	for _, row := range []struct {
		status  Status
		meaning string
	}{
		{StatusDone, "実装とテストの両方に注釈がある"},
		{StatusImplOnly, "実装に注釈があり、テストに無い"},
		{StatusTestOnly, "テストに注釈があり、実装に無い"},
		{StatusNotStarted, "注釈が無い"},
	} {
		fmt.Fprintf(&b, "| %s | %d | %s |\n", row.status, r.Count(row.status), row.meaning)
	}
	fmt.Fprintf(&b, "| 合計 | %d | |\n\n", total)

	b.WriteString("## 一覧\n\n")
	b.WriteString("| ID | 状態 | 実装 | テスト | 要件 |\n|---|---|---|---|---|\n")
	for _, e := range r.Entries {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			e.Requirement.ID, e.Status(), links(e.Impl, linkPrefix), links(e.Tests, linkPrefix), e.Requirement.Text)
	}
	return b.String()
}

// links は、注釈の位置を Markdown のリンクの並びにする。
func links(refs []Ref, prefix string) string {
	if len(refs) == 0 {
		return "-"
	}
	parts := make([]string, len(refs))
	for i, ref := range refs {
		parts[i] = fmt.Sprintf("[%s:%d](%s%s#L%d)", ref.File, ref.Line, prefix, ref.File, ref.Line)
	}
	return strings.Join(parts, "<br>")
}

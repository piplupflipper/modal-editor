package reqtrace

import (
	"strings"
	"testing"
)

const sampleRequirements = `# 要件定義

| ID | 要件 |
|---|---|
| FR-01 | カーソルを移動できる |
| FR-02 | 回数を指定できる |
| FR-03 | 削除できる |
| FR-04 | まだ作っていない |

| ID | 要件 |
|---|---|
| NFR-01 | 軽量である |
`

func TestParseRequirements(t *testing.T) {
	reqs, err := ParseRequirements(strings.NewReader(sampleRequirements))
	if err != nil {
		t.Fatalf("ParseRequirements() error = %v", err)
	}

	var ids []string
	for _, r := range reqs {
		ids = append(ids, r.ID)
	}
	if got, want := strings.Join(ids, " "), "FR-01 FR-02 FR-03 FR-04 NFR-01"; got != want {
		t.Errorf("ID の並び = %q, want %q", got, want)
	}
	if got, want := reqs[0].Text, "カーソルを移動できる"; got != want {
		t.Errorf("reqs[0].Text = %q, want %q", got, want)
	}
}

func TestParseRequirementsDuplicate(t *testing.T) {
	input := "| FR-01 | a |\n| FR-01 | b |\n"
	if _, err := ParseRequirements(strings.NewReader(input)); err == nil {
		t.Error("ID が重複しているのにエラーにならない")
	}
}

func TestParseAnnotation(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"// req: FR-01", "FR-01"},
		{"// req: FR-01, NFR-02", "FR-01 NFR-02"},
		{"//req:FR-01", "FR-01"},
		{" * req: FR-03", "FR-03"},
		{"req: FR-03", "FR-03"},
		{"// 注釈は req: FR-01 のように書く", ""}, // 行頭でなければ注釈ではない
		{"// req: <ID>", ""},             // ID の形式でなければ無視する
		{"// requirement FR-01", ""},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := strings.Join(parseAnnotation(tt.line), " "); got != tt.want {
				t.Errorf("parseAnnotation(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestScanAndBuild(t *testing.T) {
	reqs, err := ParseRequirements(strings.NewReader(sampleRequirements))
	if err != nil {
		t.Fatalf("ParseRequirements() error = %v", err)
	}
	refs, err := Scan("testdata/sample")
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}

	report := Build(reqs, refs)

	want := map[string]Status{
		"FR-01":  StatusDone,
		"FR-02":  StatusImplOnly,
		"FR-03":  StatusImplOnly,
		"FR-04":  StatusImplOnly, // Makefile の注釈は実装として数える
		"NFR-01": StatusTestOnly,
	}
	for _, e := range report.Entries {
		if got := e.Status(); got != want[e.Requirement.ID] {
			t.Errorf("%s の状態 = %v, want %v", e.Requirement.ID, got, want[e.Requirement.ID])
		}
	}

	// 文字列の中と Makefile のレシピの行の FR-99 は拾わず、コメントの FR-77 だけが未知の ID になる。
	if len(report.Unknown) != 1 || report.Unknown[0].ID != "FR-77" {
		t.Errorf("Unknown = %+v, want FR-77 の 1 件", report.Unknown)
	}

	// ブロックコメントの中の行番号が、ファイルの行番号と一致する。
	for _, e := range report.Entries {
		if e.Requirement.ID == "FR-03" {
			if got, want := e.Impl[0].Line, 12; got != want {
				t.Errorf("FR-03 の行番号 = %d, want %d", got, want)
			}
		}
		if e.Requirement.ID == "FR-04" {
			if got := e.Impl[0]; got.File != "Makefile" || got.Line != 2 {
				t.Errorf("FR-04 の注釈 = %s:%d, want Makefile:2", got.File, got.Line)
			}
		}
	}
}

func TestRender(t *testing.T) {
	report := Build(
		[]Requirement{{ID: "FR-01", Text: "移動できる"}, {ID: "FR-02", Text: "未着手の要件"}},
		[]Ref{
			{ID: "FR-01", File: "internal/a/a.go", Line: 10},
			{ID: "FR-01", File: "internal/a/a_test.go", Line: 5, IsTest: true},
		},
	)

	got := Render(report, "../")

	for _, want := range []string{
		"| 実装とテスト | 1 |",
		"| 未着手 | 1 |",
		"| FR-01 | 実装とテスト | [internal/a/a.go:10](../internal/a/a.go#L10) | [internal/a/a_test.go:5](../internal/a/a_test.go#L5) | 移動できる |",
		"| FR-02 | 未着手 | - | - | 未着手の要件 |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("出力に %q が無い:\n%s", want, got)
		}
	}
}

package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// agent/ の文書は、どれも話題として埋め込んである。文書を足して Topics に書き忘れると落ちる。
// 話題の中身は、ファイルから 1 行だけの HTML のコメントの行を除いたものである。
func TestTopicsCoverFiles(t *testing.T) {
	files := map[string]bool{"debugging.md": true, "usage.md": true}
	paths, err := filepath.Glob("agent/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range paths {
		files[filepath.ToSlash(p)] = true
	}
	for _, tp := range Topics() {
		if !files[tp.File] {
			t.Errorf("話題 %s のファイル %s が無い", tp.Name, tp.File)
		}
		delete(files, tp.File)
		data, err := os.ReadFile(tp.File)
		if err != nil {
			t.Fatal(err)
		}
		if want := strip(string(data)); tp.Text != want || strings.Contains(tp.Text, "<!-- generated:") {
			t.Errorf("話題 %s の中身がファイルと違う", tp.Name)
		}
		if tp.Summary == "" {
			t.Errorf("話題 %s に説明が無い", tp.Name)
		}
		if got, ok := LookupTopic(tp.Name); !ok || got.File != tp.File {
			t.Errorf("LookupTopic(%q) = %+v, %v", tp.Name, got, ok)
		}
	}
	for f := range files {
		t.Errorf("%s が話題に無い", f)
	}
	if _, ok := LookupTopic("nope"); ok {
		t.Error("知らない話題が見つかった")
	}
}

// 文書の中の「vi-imitation --docs <話題> <節>」の案内が、どれも本当にある話題と節を指す。
// usage.md の見出しを変えて、案内を直し忘れると落ちる。
func TestDocsReferences(t *testing.T) {
	ref := regexp.MustCompile("`vi-imitation --docs ([a-z-]+)(?: ([^`]+))?`")
	for _, tp := range Topics() {
		for _, m := range ref.FindAllStringSubmatch(tp.Text, -1) {
			target, ok := LookupTopic(m[1])
			if !ok {
				t.Errorf("%s: 知らない話題 %q を指す", tp.Name, m[0])
				continue
			}
			sec := strings.Trim(m[2], "'")
			if sec == "" || strings.HasPrefix(sec, "<") && strings.HasSuffix(sec, ">") && !strings.Contains(sec, " ") {
				continue // 節の無い案内と、<型の名前> のような置き場所
			}
			if _, ok := Section(target.Text, sec); !ok {
				t.Errorf("%s: %s の節 %q が無い", tp.Name, m[1], sec)
			}
		}
	}
}

// 見出しは、コードの囲みの中の # で始まる行を含まない。節は、同じかより上の段の次の見出しの手前までで、下の段の節を含む。
func TestSection(t *testing.T) {
	text := "# 題\n\n## `a` と b\n\n本文\n\n```sh\n# コメント\n```\n\n### 子\n\n子の本文\n\n## 次\n\n次の本文\n"
	hs := Headings(text)
	var titles []string
	for _, h := range hs {
		titles = append(titles, h.Title)
	}
	if got := strings.Join(titles, "|"); got != "題|`a` と b|子|次" {
		t.Errorf("見出し = %q", got)
	}
	want := "## `a` と b\n\n本文\n\n```sh\n# コメント\n```\n\n### 子\n\n子の本文\n"
	for _, title := range []string{"`a` と b", "a と b"} {
		if got, ok := Section(text, title); !ok || got != want {
			t.Errorf("Section(%q) = %q, %v, want %q", title, got, ok, want)
		}
	}
	if got, ok := Section(text, "次"); !ok || got != "## 次\n\n次の本文\n" {
		t.Errorf("Section(次) = %q, %v", got, ok)
	}
	if _, ok := Section(text, "コメント"); ok {
		t.Error("コードの囲みの中の行が節になった")
	}
}

package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 整形ツールの設定ファイル（pyproject.toml、ruff.toml、.prettierrc の YAML の形）を読む、必要な項目だけの小さな読み手（#349）。
//
// TOML と YAML の外部ライブラリは足さない（dev-cycle 2.2。issue の推奨）。読みたいのは、表の名前と、文字列、整数、真偽の値だけなので、
// 次のとおりに限って読む。読めない書き方の値（配列、日時、浮動小数点数、複数行の文字列）は、読み飛ばして otherValue にする。
// どの値にも当たらない行は、壊れた設定ファイルとして誤りにする。

// bom は、UTF-8 の BOM である。設定ファイルの先頭にあれば除いて読む。
const bom = "\xef\xbb\xbf"

// otherValue は、読み手が中身を読まない値（配列、日時、浮動小数点数、複数行の文字列、YAML の入れ子）である。
type otherValue struct{}

// parseTOML は、TOML の文書 src を、点で区切った完全な鍵（"tool.ruff.format.indent-style"）から値への対応にする。
// 値は string、int64、bool、otherValue のどれかである。
//
// 読むのは、表の見出し（[a.b] と [[a.b]]）、鍵（裸の鍵、引用した鍵、点で区切った鍵）と値の組、インラインの表（{ k = v }。中の鍵も
// 完全な鍵にする）、文字列（基本の文字列と、リテラルの文字列）、整数（_ と 0x、0o、0b を含む）、真偽である。
// 配列（複数行を含む）、複数行の文字列、浮動小数点数、日時は読み飛ばす。[[a.b]] の中の鍵は "a.b[]." の下に置き、[a.b] と混ぜない。
// 同じ鍵を 2 回書いても誤りにしない（後の値を使う）。
func parseTOML(src string) (map[string]any, error) {
	p := &tomlParser{s: strings.TrimPrefix(src, bom), line: 1, out: map[string]any{}}
	if err := p.document(); err != nil {
		return nil, fmt.Errorf("%d 行目: %w", p.line, err)
	}
	return p.out, nil
}

type tomlParser struct {
	s    string
	i    int
	line int
	out  map[string]any
}

func (p *tomlParser) eof() bool { return p.i >= len(p.s) }

func (p *tomlParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

// spaces は、空白とタブを読み飛ばす。
func (p *tomlParser) spaces() {
	for !p.eof() && (p.s[p.i] == ' ' || p.s[p.i] == '\t') {
		p.i++
	}
}

// blank は、空白、改行、注釈を読み飛ばす。
func (p *tomlParser) blank() {
	for !p.eof() {
		switch p.s[p.i] {
		case ' ', '\t', '\r':
			p.i++
		case '\n':
			p.i++
			p.line++
		case '#':
			p.comment()
		default:
			return
		}
	}
}

// comment は、注釈を行の終わりの手前まで読み飛ばす。
func (p *tomlParser) comment() {
	for !p.eof() && p.s[p.i] != '\n' {
		p.i++
	}
}

// eol は、値の後の空白と注釈を読み飛ばし、行の終わりか文書の終わりであることを確かめる。
func (p *tomlParser) eol() error {
	p.spaces()
	if p.peek() == '#' {
		p.comment()
	}
	if p.peek() == '\r' {
		p.i++
	}
	if p.eof() {
		return nil
	}
	if p.s[p.i] != '\n' {
		return fmt.Errorf("値の後に %q がある", p.rest())
	}
	return nil
}

// rest は、誤りの知らせに使う、今の位置から行の終わりまでの文字列（40 バイトまで）である。
func (p *tomlParser) rest() string {
	r, _, _ := strings.Cut(p.s[p.i:], "\n")
	if len(r) > 40 {
		r = r[:40]
	}
	return r
}

func (p *tomlParser) document() error {
	prefix := ""
	for {
		p.blank()
		if p.eof() {
			return nil
		}
		if p.peek() == '[' {
			p.i++
			array := p.peek() == '['
			if array {
				p.i++
			}
			p.spaces()
			key, err := p.key()
			if err != nil {
				return err
			}
			p.spaces()
			closing := "]"
			if array {
				closing = "]]"
			}
			if !strings.HasPrefix(p.s[p.i:], closing) {
				return fmt.Errorf("表の見出しが %s で閉じていない", closing)
			}
			p.i += len(closing)
			prefix = key + "."
			if array {
				prefix = key + "[]."
			}
			// 鍵の無い表（[tool.ruff] の 1 行だけ）も、表があることを hasTable で見分けられるように印を入れる（PR #352 のレビューの推奨 3）。
			p.out[prefix] = otherValue{}
			if err := p.eol(); err != nil {
				return err
			}
			continue
		}
		if err := p.keyValue(prefix); err != nil {
			return err
		}
		if err := p.eol(); err != nil {
			return err
		}
	}
}

// keyValue は、鍵と値の組を 1 つ読み、prefix を付けた鍵で out に入れる。
func (p *tomlParser) keyValue(prefix string) error {
	key, err := p.key()
	if err != nil {
		return err
	}
	p.spaces()
	if p.peek() != '=' {
		return fmt.Errorf("鍵 %s の後に = が無い", key)
	}
	p.i++
	p.spaces()
	return p.value(prefix + key)
}

// key は、点で区切った鍵を読み、点で繋いだ文字列にする。
func (p *tomlParser) key() (string, error) {
	var parts []string
	for {
		p.spaces()
		var part string
		switch c := p.peek(); c {
		case '"', '\'':
			s, err := p.singleLineString(c)
			if err != nil {
				return "", err
			}
			part = s
		default:
			start := p.i
			for !p.eof() && isBareKeyChar(p.s[p.i]) {
				p.i++
			}
			if start == p.i {
				return "", fmt.Errorf("鍵が無い: %q", p.rest())
			}
			part = p.s[start:p.i]
		}
		parts = append(parts, part)
		p.spaces()
		if p.peek() != '.' {
			return strings.Join(parts, "."), nil
		}
		p.i++
	}
}

func isBareKeyChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// singleLineString は、1 行の基本の文字列（"..."）かリテラルの文字列（'...'）を読む。
func (p *tomlParser) singleLineString(q byte) (string, error) {
	start := p.i
	p.i++
	for !p.eof() {
		switch c := p.s[p.i]; {
		case c == '\n':
			return "", errors.New("文字列が行の中で閉じていない")
		case c == '\\' && q == '"':
			p.i += 2
		case c == q:
			p.i++
			lit := p.s[start:p.i]
			if q == '\'' {
				return lit[1 : len(lit)-1], nil
			}
			return tomlUnescape(lit[1 : len(lit)-1])
		default:
			p.i++
		}
	}
	return "", errors.New("文字列が閉じていない")
}

// tomlUnescape は、基本の文字列の中身 body の逃がし（\b \t \n \f \r \" \\ \uXXXX \UXXXXXXXX と、TOML 1.1 の \e \xHH）を解く。
// ruff 0.15.16 は TOML 1.1 の書き方を受け付ける（PR #352 のレビューの推奨 4）。
func tomlUnescape(body string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(body) {
			return "", errors.New("文字列の終わりに \\ がある")
		}
		i++
		hex := 0
		switch body[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case 'e':
			b.WriteByte(0x1b)
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'x':
			hex = 2
		case 'u':
			hex = 4
		case 'U':
			hex = 8
		default:
			return "", fmt.Errorf("文字列の逃がし \\%c を読めない", body[i])
		}
		if hex == 0 {
			continue
		}
		if i+1+hex > len(body) {
			return "", errors.New("文字列の逃がしの 16 進の桁が足りない")
		}
		n, err := strconv.ParseInt(body[i+1:i+1+hex], 16, 32)
		if err != nil {
			return "", fmt.Errorf("文字列の逃がし \\%s を読めない", body[i:i+1+hex])
		}
		b.WriteRune(rune(n))
		i += hex
	}
	return b.String(), nil
}

// tomlDate は、日時の値の始まり（1979-05-27 と 07:32:00）である。
var tomlDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}|\d{2}:\d{2})`)

// value は、値を 1 つ読み、鍵 key で out に入れる。インラインの表は、中の鍵を key の下に入れる。
func (p *tomlParser) value(key string) error {
	switch c := p.peek(); {
	case p.atMultiline():
		if err := p.skipMultiline(); err != nil {
			return err
		}
		p.out[key] = otherValue{}
	case c == '"' || c == '\'':
		s, err := p.singleLineString(c)
		if err != nil {
			return err
		}
		p.out[key] = s
	case c == '[':
		if err := p.skipArray(); err != nil {
			return err
		}
		p.out[key] = otherValue{}
	case c == '{':
		return p.inlineTable(key)
	default:
		start := p.i
		for !p.eof() && !strings.ContainsRune(" \t\r\n,]}#", rune(p.s[p.i])) {
			p.i++
		}
		tok := p.s[start:p.i]
		// 日付と時刻の間の空白（1979-05-27 07:32:00Z）。
		if tomlDate.MatchString(tok) && len(tok) == 10 && strings.HasPrefix(p.s[p.i:], " ") && len(p.s) > p.i+1 && p.s[p.i+1] >= '0' && p.s[p.i+1] <= '9' {
			p.i++
			for !p.eof() && !strings.ContainsRune(" \t\r\n,]}#", rune(p.s[p.i])) {
				p.i++
			}
		}
		v, err := tomlScalar(tok)
		if err != nil {
			return err
		}
		p.out[key] = v
	}
	return nil
}

// atMultiline は、今の位置が複数行の文字列（""" か ”'）の始まりかを返す。
func (p *tomlParser) atMultiline() bool {
	return strings.HasPrefix(p.s[p.i:], `"""`) || strings.HasPrefix(p.s[p.i:], "'''")
}

// skipMultiline は、複数行の文字列を閉じるまで読み飛ばす。中身は読まない。
func (p *tomlParser) skipMultiline() error {
	q := p.s[p.i : p.i+3]
	body := p.s[p.i+3:]
	end := -1
	for j := 0; j+3 <= len(body); j++ {
		if q[0] == '"' && body[j] == '\\' {
			j++
			continue
		}
		if strings.HasPrefix(body[j:], q) {
			end = j
			break
		}
	}
	if end < 0 {
		return errors.New("複数行の文字列が閉じていない")
	}
	p.line += strings.Count(body[:end], "\n")
	p.i += 3 + end + 3
	// 閉じる引用符の直後に 1 つか 2 つの引用符を続けてよい（"""a"""" は a" になる）。
	for k := 0; k < 2 && p.peek() == q[0]; k++ {
		p.i++
	}
	return nil
}

// tomlScalar は、引用しない値（真偽、整数、浮動小数点数、日時）を読む。
func tomlScalar(tok string) (any, error) {
	switch tok {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "":
		return nil, errors.New("値が無い")
	}
	t := strings.ReplaceAll(tok, "_", "")
	// 10 進の整数は、符号の後が 0 で始まらない（0 自身を除く）。0x、0o、0b は符号を付けない。
	if digits := strings.TrimPrefix(strings.TrimPrefix(t, "+"), "-"); digits != "" && strings.Trim(digits, "0123456789") == "" {
		if digits != "0" && digits[0] == '0' {
			return nil, fmt.Errorf("整数 %q の先頭に 0 がある", tok)
		}
		if n, err := strconv.ParseInt(strings.TrimPrefix(t, "+"), 10, 64); err == nil {
			return n, nil
		}
	}
	if len(t) > 2 && t[0] == '0' && strings.ContainsRune("xob", rune(t[1])) {
		if n, err := strconv.ParseInt(t, 0, 64); err == nil {
			return n, nil
		}
	}
	if _, err := strconv.ParseFloat(t, 64); err == nil || tomlDate.MatchString(tok) {
		return otherValue{}, nil
	}
	switch strings.TrimLeft(tok, "+-") {
	case "inf", "nan":
		return otherValue{}, nil
	}
	return nil, fmt.Errorf("値 %q を読めない", tok)
}

// skipArray は、配列を、入れ子の配列、インラインの表、文字列、注釈、改行を含めて閉じるまで読み飛ばす。
func (p *tomlParser) skipArray() error {
	depth := 0
	for !p.eof() {
		switch c := p.s[p.i]; c {
		case '[', '{':
			depth++
			p.i++
		case ']', '}':
			depth--
			p.i++
			if depth == 0 {
				return nil
			}
		case '"', '\'':
			if p.atMultiline() {
				if err := p.skipMultiline(); err != nil {
					return err
				}
				continue
			}
			if _, err := p.singleLineString(c); err != nil {
				return err
			}
		case '#':
			p.comment()
		case '\n':
			p.line++
			p.i++
		default:
			p.i++
		}
	}
	return errors.New("配列が閉じていない")
}

// inlineTable は、インラインの表（{ k = v, ... }）を読み、中の鍵を key の下に入れる。
// TOML 1.1 の書き方（表の中の改行と注釈、閉じる前のカンマ）も読む（ruff 0.15.16 が受け付ける。PR #352 のレビューの推奨 4）。
func (p *tomlParser) inlineTable(key string) error {
	p.i++ // {
	for {
		p.blank()
		if p.peek() == '}' {
			p.i++
			return nil
		}
		if err := p.keyValue(key + "."); err != nil {
			return err
		}
		p.blank()
		switch p.peek() {
		case ',':
			p.i++
		case '}':
			p.i++
			return nil
		default:
			return errors.New("インラインの表が閉じていない")
		}
	}
}

// parseYAMLTop は、YAML の文書 src の最上位のマッピングの、引用しない値か引用した値だけを読む（.prettierrc の YAML の形。#349）。
// 値は string、int64、bool、otherValue のどれかである。
//
// 読むのは、行の先頭から書いた「鍵: 値」の行だけである。字下げした行（入れ子のマッピング、列、ブロックの文字列の続き）は読み飛ばし、
// その上の鍵の値は otherValue にする。注釈（# の後）、文書の区切り（--- と ...）は読み飛ばす。
// 真偽は true と false（YAML 1.2 の core schema の True、TRUE を含む）、整数は 10 進だけを読む。
// 最上位が「鍵: 値」の形でない行（最上位の列 "- a" など）は、prettier の設定の形でないので誤りにする。
// 文書が流れの形のマッピング（{ useTabs: true, tabWidth: 4 }。複数行でもよい）なら、parseYAMLFlow で読む。
// 2 つ目より後の最上位の行が { か [ で始まるなど、読めない形は誤りにする（黙って読み飛ばさない。PR #352 のレビューの推奨 5）。
func parseYAMLTop(src string) (map[string]any, error) {
	out := map[string]any{}
	first := true
	lines := strings.Split(strings.TrimPrefix(src, bom), "\n")
	for n, line := range lines {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
			continue
		case trimmed == "---" || trimmed == "...":
			continue
		case first && strings.HasPrefix(trimmed, "{"):
			return parseYAMLFlow(strings.Join(lines[n:], "\n"))
		case line[0] == ' ' || line[0] == '\t':
			if first {
				return nil, fmt.Errorf("%d 行目: 最上位の行が字下げしてある", n+1)
			}
			continue
		case strings.ContainsRune("{[]}", rune(line[0])):
			return nil, fmt.Errorf("%d 行目: 最上位が「鍵: 値」の形でない: %q", n+1, line)
		}
		first = false
		key, val, err := yamlKeyValue(line)
		if err != nil {
			return nil, fmt.Errorf("%d 行目: %w", n+1, err)
		}
		out[key] = val
	}
	return out, nil
}

// parseYAMLFlow は、流れの形のマッピング 1 つだけの YAML の文書 src（{ k: v, 'k2': "v2" }。複数行と注釈と閉じる前のカンマを許す）を読む。
// 値は string、int64、bool、otherValue（入れ子の流れの形）のどれかである。閉じた後に注釈と空白のほかが続けば誤りにする。
// prettier 3.6.2 は .prettierrc を YAML として読むので、この形（JSON5 に似た、鍵を引用しない形）も使える（PR #352 のレビューの推奨 5）。
func parseYAMLFlow(src string) (map[string]any, error) {
	p := &yamlFlow{s: src}
	p.blank()
	if p.peek() != '{' {
		return nil, errors.New("流れの形のマッピングが { で始まっていない")
	}
	p.i++
	out := map[string]any{}
	for {
		p.blank()
		if p.peek() == '}' {
			p.i++
			break
		}
		key, err := p.scalar(":")
		if err != nil {
			return nil, err
		}
		if p.peek() != ':' {
			return nil, fmt.Errorf("鍵 %s の後に : が無い", key)
		}
		p.i++
		p.blank()
		var v any
		switch p.peek() {
		case '{', '[':
			if err := p.skipNested(); err != nil {
				return nil, err
			}
			v = otherValue{}
		case '"', '\'':
			s, err := p.scalar(",}")
			if err != nil {
				return nil, err
			}
			v = s
		default:
			s, err := p.scalar(",}")
			if err != nil {
				return nil, err
			}
			if v, err = yamlScalar(s); err != nil {
				return nil, err
			}
		}
		out[key] = v
		p.blank()
		switch p.peek() {
		case ',':
			p.i++
		case '}':
		default:
			return nil, errors.New("流れの形のマッピングが閉じていない")
		}
	}
	p.blank()
	if !p.eof() {
		return nil, fmt.Errorf("流れの形のマッピングの後に %q がある", strings.TrimSpace(p.s[p.i:]))
	}
	return out, nil
}

// yamlFlow は、parseYAMLFlow の読む位置である。
type yamlFlow struct {
	s string
	i int
}

func (p *yamlFlow) eof() bool { return p.i >= len(p.s) }

func (p *yamlFlow) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.i]
}

// blank は、空白、改行、注釈（行の先頭か空白の後の # から行の終わりまで）を読み飛ばす。
func (p *yamlFlow) blank() {
	for !p.eof() {
		switch p.s[p.i] {
		case ' ', '\t', '\r', '\n':
			p.i++
		case '#':
			for !p.eof() && p.s[p.i] != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

// scalar は、引用した文字列か、stops のどれかの文字の手前までの引用しない文字列を読む。引用しない文字列の前後の空白は除く。
func (p *yamlFlow) scalar(stops string) (string, error) {
	if q := p.peek(); q == '"' || q == '\'' {
		start := p.i
		p.i++
		for !p.eof() {
			c := p.s[p.i]
			if c == '\\' && q == '"' {
				p.i += 2
				continue
			}
			p.i++
			if c != q {
				continue
			}
			if q == '\'' && p.peek() == '\'' { // '' は ' 1 つ
				p.i++
				continue
			}
			lit := p.s[start:p.i]
			p.blank()
			if q == '\'' {
				return strings.ReplaceAll(lit[1:len(lit)-1], "''", "'"), nil
			}
			s, err := strconv.Unquote(lit)
			if err != nil {
				return "", fmt.Errorf("文字列 %s を読めない", lit)
			}
			return s, nil
		}
		return "", errors.New("文字列が閉じていない")
	}
	start := p.i
	for !p.eof() && !strings.ContainsRune(stops+"\n{}[]", rune(p.s[p.i])) {
		if p.s[p.i] == '#' && p.i > start && (p.s[p.i-1] == ' ' || p.s[p.i-1] == '\t') {
			break
		}
		p.i++
	}
	s := strings.TrimSpace(p.s[start:p.i])
	p.blank()
	if s == "" {
		return "", fmt.Errorf("流れの形のマッピングに値が無い: %q", p.rest())
	}
	return s, nil
}

// rest は、誤りの知らせに使う、今の位置から 40 バイトまでの文字列である。
func (p *yamlFlow) rest() string {
	r := p.s[p.i:]
	if len(r) > 40 {
		r = r[:40]
	}
	return r
}

// skipNested は、入れ子の流れの形（{...} と [...]）を、引用した文字列を含めて閉じるまで読み飛ばす。
func (p *yamlFlow) skipNested() error {
	depth := 0
	for !p.eof() {
		switch c := p.s[p.i]; c {
		case '{', '[':
			depth++
			p.i++
		case '}', ']':
			depth--
			p.i++
			if depth == 0 {
				return nil
			}
		case '"', '\'':
			if _, err := p.scalar(""); err != nil {
				return err
			}
		default:
			p.i++
		}
	}
	return errors.New("流れの形が閉じていない")
}

// yamlKeyValue は、最上位の「鍵: 値」の行を読む。
func yamlKeyValue(line string) (string, any, error) {
	var key, rest string
	switch line[0] {
	case '"', '\'':
		end := strings.IndexByte(line[1:], line[0])
		if end < 0 {
			return "", nil, errors.New("鍵の引用符が閉じていない")
		}
		key, rest = line[1:1+end], line[2+end:]
		rest = strings.TrimLeft(rest, " \t")
		if !strings.HasPrefix(rest, ":") {
			return "", nil, fmt.Errorf("鍵 %s の後に : が無い", key)
		}
		rest = rest[1:]
	default:
		// 鍵の終わりは、後に空白かタブか行の終わりが続く最初の : である（YAML の plain scalar の鍵）。
		i := -1
		for j := 0; j < len(line); j++ {
			if line[j] == ':' && (j+1 == len(line) || line[j+1] == ' ' || line[j+1] == '\t') {
				i = j
				break
			}
		}
		if i <= 0 || line == "-" || strings.HasPrefix(line, "- ") {
			return "", nil, fmt.Errorf("最上位が「鍵: 値」の形でない: %q", line)
		}
		key, rest = strings.TrimSpace(line[:i]), line[i+1:]
	}
	val, err := yamlScalar(rest)
	return key, val, err
}

// yamlScalar は、最上位の鍵の値の文字列 s を読む。
// 値の前のアンカー（&w 4）は除いて読む（prettier 3.6.2 は tabWidth: &w 4 を 4 と読む。PR #352 の再レビューの推奨 4）。
func yamlScalar(s string) (any, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "&") {
		_, rest, _ := strings.Cut(s, " ")
		s = strings.TrimSpace(rest)
	}
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		q := s[0]
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return nil, errors.New("値の引用符が閉じていない")
		}
		body, after := s[1:1+end], strings.TrimSpace(s[2+end:])
		if after != "" && !strings.HasPrefix(after, "#") {
			return nil, fmt.Errorf("値の後に %q がある", after)
		}
		if q == '"' {
			u, err := strconv.Unquote(`"` + body + `"`)
			if err != nil {
				return nil, fmt.Errorf("値 %q を読めない", s)
			}
			return u, nil
		}
		return body, nil
	}
	if i := strings.Index(s, " #"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if strings.HasPrefix(s, "#") {
		s = ""
	}
	switch s {
	case "", "|", ">", "|-", ">-", "|+", ">+":
		return otherValue{}, nil // 入れ子か、ブロックの文字列（続きの字下げした行は読み飛ばす）
	case "true", "True", "TRUE":
		return true, nil
	case "false", "False", "FALSE":
		return false, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	if strings.ContainsAny(s[:1], "[{&*!") {
		return otherValue{}, nil // 流れの形、アンカー、タグ
	}
	return s, nil
}

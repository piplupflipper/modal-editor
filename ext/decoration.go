package ext

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// decoration/set の JSON の形（基本設計 6.1）。
//
// 範囲の一覧は、範囲ごとのオブジェクトの配列ではなく、整数の列 data と色の種類の名前の表 styles で表す。
// LSP の semantic tokens と同じく、範囲の開始を直前の範囲の開始からの差で書き、1 つの範囲に 5 つの整数を使う。
// オブジェクトの配列では 1 つの範囲が 90 バイトほどになり、約 6 MB の JSON の一覧が 74 MB になって本体の受信の上限（64 MiB）を超えた（#47 のレビュー）。
//
//	{"buffer":1,"version":3,"styles":["keyword","string"],"data":[0,0,0,7,0, 1,4,0,5,1]}
//
// data の i 番目の範囲（0 から数える）は data[5i:5i+5] の次の 5 つである。直前の範囲の開始は、i が 0 なら行 0 のバイト 0 とする。
//
//	deltaLine  開始の行 - 直前の範囲の開始の行
//	deltaStart deltaLine が 0 なら 開始のバイト - 直前の範囲の開始のバイト、そうでなければ 開始のバイト
//	lineSpan   終わりの行 - 開始の行
//	end        lineSpan が 0 なら 終わりのバイト - 開始のバイト（範囲の長さ）、そうでなければ 終わりのバイト
//	style      styles の添字
//
// 整数は符号付きで、範囲の順序と値に制約は無い（並んでいなくても、行をまたいでも、終わりが開始より前でも表せる）。
// 送る側の一覧（DecorationSetParams.Decorations）は、順序を含めてそのまま読み戻せる。
// 本体は、重なった範囲を一覧の後ろのものほど上に描くので、順序には意味がある。
const decorationFields = 5

// decorationSetWire は、decoration/set の params の JSON の形である。
type decorationSetWire struct {
	Buffer  BufferID        `json:"buffer"`
	Version Version         `json:"version"`
	Styles  []string        `json:"styles"`
	Data    json.RawMessage `json:"data"`
}

// MarshalJSON は、p を decoration/set の JSON の形（範囲を整数の列にしたもの）にする。
// 色の種類の名前の表 styles は、一覧に初めて現れた順に並べる。
func (p DecorationSetParams) MarshalJSON() ([]byte, error) {
	index := map[string]int{}
	styles := []string{}
	for _, d := range p.Decorations {
		if _, ok := index[d.Style]; !ok {
			index[d.Style] = len(styles)
			styles = append(styles, d.Style)
		}
	}
	stylesJSON, err := json.Marshal(styles)
	if err != nil {
		return nil, err
	}
	// 1 つの範囲はたいてい 12 バイトに収まる。
	b := make([]byte, 0, 64+len(stylesJSON)+12*len(p.Decorations))
	b = append(b, `{"buffer":`...)
	b = strconv.AppendUint(b, uint64(p.Buffer), 10)
	b = append(b, `,"version":`...)
	b = strconv.AppendUint(b, uint64(p.Version), 10)
	b = append(b, `,"styles":`...)
	b = append(b, stylesJSON...)
	b = append(b, `,"data":[`...)
	var prev Position
	for i, d := range p.Decorations {
		if i > 0 {
			b = append(b, ',')
		}
		for j, v := range encodeDecoration(prev, d, index[d.Style]) {
			if j > 0 {
				b = append(b, ',')
			}
			b = strconv.AppendInt(b, int64(v), 10)
		}
		prev = d.Range.Start
	}
	b = append(b, "]}"...)
	return b, nil
}

// encodeDecoration は、直前の範囲の開始が prev のときの、d の 5 つの整数を返す。
// 差は int の範囲で桁あふれしてもよい（decodeDecoration が同じく桁あふれして元の値に戻る）。
func encodeDecoration(prev Position, d Decoration, style int) [decorationFields]int {
	s, e := d.Range.Start, d.Range.End
	v := [decorationFields]int{s.Line - prev.Line, s.Byte, e.Line - s.Line, e.Byte, style}
	if v[0] == 0 {
		v[1] -= prev.Byte
	}
	if v[2] == 0 {
		v[3] -= s.Byte
	}
	return v
}

// decodeDecoration は、直前の範囲の開始が prev のときの 5 つの整数 v から、範囲を求める。encodeDecoration の逆である。
func decodeDecoration(prev Position, v [decorationFields]int) Range {
	var r Range
	r.Start.Line = prev.Line + v[0]
	r.Start.Byte = v[1]
	if v[0] == 0 {
		r.Start.Byte += prev.Byte
	}
	r.End.Line = r.Start.Line + v[2]
	r.End.Byte = v[3]
	if v[2] == 0 {
		r.End.Byte += r.Start.Byte
	}
	return r
}

// UnmarshalJSON は、decoration/set の JSON の形を読む。
// data の長さが 5 の倍数でないか、style が styles の外を指せば誤りを返す。
// 一覧が空なら、Decorations は nil ではなく長さ 0 のスライスにする。
func (p *DecorationSetParams) UnmarshalJSON(b []byte) error {
	var w decorationSetWire
	if err := json.Unmarshal(b, &w); err != nil {
		return err
	}
	data, err := parseInts(w.Data)
	if err != nil {
		return fmt.Errorf("decoration/set data: %w", err)
	}
	if len(data)%decorationFields != 0 {
		return fmt.Errorf("decoration/set data: length %d is not a multiple of %d", len(data), decorationFields)
	}
	decs := make([]Decoration, len(data)/decorationFields)
	var prev Position
	for i := range decs {
		v := [decorationFields]int(data[i*decorationFields : (i+1)*decorationFields])
		if v[4] < 0 || v[4] >= len(w.Styles) {
			return fmt.Errorf("decoration/set data: style index %d out of range [0, %d)", v[4], len(w.Styles))
		}
		r := decodeDecoration(prev, v)
		decs[i] = Decoration{Range: r, Style: w.Styles[v[4]]}
		prev = r.Start
	}
	*p = DecorationSetParams{Buffer: w.Buffer, Version: w.Version, Decorations: decs}
	return nil
}

var errNotIntArray = errors.New("not an array of integers")

// parseInts は、JSON の整数の配列を読む。null と、項目の無い場合は空の列を返す。
// encoding/json で []int に読むより速い（一覧は数十万の範囲、数百万の整数になる）。
// 呼ぶ前に encoding/json が JSON として正しいことを確かめているので、ここでは整数の配列であることだけを確かめる。
func parseInts(b []byte) ([]int, error) {
	i := skipSpace(b, 0)
	if i == len(b) || string(b[i:]) == "null" {
		return nil, nil
	}
	if b[i] != '[' {
		return nil, errNotIntArray
	}
	// 整数の数は、区切りの数から見積もる。
	n := 0
	for _, c := range b {
		if c == ',' {
			n++
		}
	}
	out := make([]int, 0, n+1)
	i = skipSpace(b, i+1)
	if i < len(b) && b[i] == ']' {
		return out, nil
	}
	for {
		j := i
		if j < len(b) && b[j] == '-' {
			j++
		}
		for j < len(b) && b[j] >= '0' && b[j] <= '9' {
			j++
		}
		v, err := strconv.Atoi(string(b[i:j]))
		if err != nil {
			return nil, errNotIntArray
		}
		out = append(out, v)
		i = skipSpace(b, j)
		if i == len(b) {
			return nil, errNotIntArray
		}
		switch b[i] {
		case ',':
			i = skipSpace(b, i+1)
		case ']':
			return out, nil
		default:
			return nil, errNotIntArray
		}
	}
}

// skipSpace は、b[i:] の先頭の JSON の空白を飛ばした位置を返す。
func skipSpace(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\t' || b[i] == '\n' || b[i] == '\r') {
		i++
	}
	return i
}

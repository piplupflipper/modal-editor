package ext

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"reflect"
	"testing"
)

func rng(sl, sb, el, eb int) Range {
	return Range{Start: Position{Line: sl, Byte: sb}, End: Position{Line: el, Byte: eb}}
}

// TestDecorationSetJSON は、decoration/set の JSON の形（範囲を 5 つの整数にした列）を、典型の一覧で確かめる。
func TestDecorationSetJSON(t *testing.T) {
	p := DecorationSetParams{Buffer: 2, Version: 9, Decorations: []Decoration{
		{Range: rng(0, 0, 0, 7), Style: "keyword"},
		{Range: rng(0, 8, 0, 12), Style: "function"},
		{Range: rng(2, 1, 4, 3), Style: "comment"}, // 行をまたぐ
		{Range: rng(4, 5, 4, 6), Style: "keyword"},
	}}
	roundTrip(t, p, `{"buffer":2,"version":9,"styles":["keyword","function","comment"],"data":[0,0,0,7,0,0,8,0,4,1,2,1,2,3,2,2,5,0,1,0]}`)
	roundTrip(t, DecorationSetParams{Buffer: 1, Version: 1, Decorations: []Decoration{}}, `{"buffer":1,"version":1,"styles":[],"data":[]}`)
}

// TestDecorationSetJSONAnyOrder は、並んでいない範囲、終わりが開始より前の範囲、負の値、int の端の値も、順序を含めて読み戻せることを確かめる。
func TestDecorationSetJSONAnyOrder(t *testing.T) {
	r := rand.New(rand.NewPCG(53, 1)) //nolint:gosec // 再現できる無作為な一覧を作るためで、暗号の用途ではない
	vals := []int{0, 1, -1, 7, 1000, math.MaxInt, math.MinInt}
	pick := func() int {
		if r.IntN(2) == 0 {
			return vals[r.IntN(len(vals))]
		}
		return r.IntN(50)
	}
	styles := []string{"keyword", "string", "", "名前"}
	for range 200 {
		n := r.IntN(20)
		decs := make([]Decoration, n)
		for i := range decs {
			decs[i] = Decoration{Range: rng(pick(), pick(), pick(), pick()), Style: styles[r.IntN(len(styles))]}
		}
		p := DecorationSetParams{Buffer: BufferID(r.Uint64()), Version: Version(r.Uint64()), Decorations: decs}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var back DecorationSetParams
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("Unmarshal(%s): %v", data, err)
		}
		if !reflect.DeepEqual(back, p) {
			t.Fatalf("Unmarshal(%s) = %+v, want %+v", data, back, p)
		}
	}
}

// TestDecorationSetJSONSpaces は、空白を含む JSON と、空の data を読めることを確かめる。
func TestDecorationSetJSONSpaces(t *testing.T) {
	var p DecorationSetParams
	if err := json.Unmarshal([]byte(`{ "buffer": 1, "version": 2, "styles": [ "a", "b" ], "data": [ 0 , -1,0,3, 1 ,
		1,2,0,1,0 ] }`), &p); err != nil {
		t.Fatal(err)
	}
	want := []Decoration{{Range: rng(0, -1, 0, 2), Style: "b"}, {Range: rng(1, 2, 1, 3), Style: "a"}}
	if !reflect.DeepEqual(p.Decorations, want) {
		t.Errorf("Decorations = %+v, want %+v", p.Decorations, want)
	}
	for _, s := range []string{`{"buffer":1,"version":2,"styles":[],"data":[]}`, `{"buffer":1,"version":2,"data":[ ]}`} {
		p = DecorationSetParams{}
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			t.Fatalf("Unmarshal(%s): %v", s, err)
		}
		if p.Decorations == nil || len(p.Decorations) != 0 || p.Buffer != 1 {
			t.Errorf("Unmarshal(%s) = %+v, want an empty list", s, p)
		}
	}
}

// TestDecorationSetJSONErrors は、整数の列として読めない data と、styles の外を指す style を誤りにすることを確かめる。
func TestDecorationSetJSONErrors(t *testing.T) {
	for _, data := range []string{
		`[0,0,0,1]`,                      // 5 の倍数でない
		`[0,0,0,1,1]`,                    // styles の外
		`[0,0,0,1,-1]`,                   // 負の添字
		`[0,0,0,1.5,0]`,                  // 整数でない
		`[0,0,0,1e2,0]`,                  // 指数の表記
		`[0,0,0,"1",0]`,                  // 文字列
		`[0,0,0,1,0,]`,                   // encoding/json が誤りにする
		`{"a":1}`,                        // 配列でない
		`[0,0,0,99999999999999999999,0]`, // int に収まらない
		`null`,
	} {
		var p DecorationSetParams
		err := json.Unmarshal([]byte(`{"buffer":1,"version":1,"styles":["a"],"data":`+data+`}`), &p)
		if err == nil {
			t.Errorf("data %s: Unmarshal succeeded with %+v, want an error", data, p)
		}
	}
}

// TestDecorationSetJSONOldFormat は、data の無い通知と、API 0.1.0 までの形（decorations）の通知を、空の一覧ではなく誤りにすることを確かめる。
func TestDecorationSetJSONOldFormat(t *testing.T) {
	for _, s := range []string{
		`{"buffer":1,"version":3,"decorations":[{"range":{"start":{"line":0,"byte":0},"end":{"line":0,"byte":1}},"style":"keyword"}]}`,
		`{"buffer":1,"version":3,"decorations":[]}`,
		`{"buffer":1,"version":3,"styles":[],"data":[],"decorations":[]}`,
		`{"buffer":1,"version":3}`,
		`{"buffer":1,"version":3,"styles":[]}`,
	} {
		var p DecorationSetParams
		if err := json.Unmarshal([]byte(s), &p); err == nil {
			t.Errorf("Unmarshal(%s) succeeded with %+v, want an error", s, p)
		}
	}
}

// TestDecorationSetJSONSize は、JSONSize が MarshalJSON の結果の長さと一致することを確かめる。
func TestDecorationSetJSONSize(t *testing.T) {
	r := rand.New(rand.NewPCG(53, 2)) //nolint:gosec // 再現できる無作為な一覧を作るためで、暗号の用途ではない
	vals := []int{0, 1, -1, 9, 10, -10, 99999, math.MaxInt, math.MinInt}
	styles := []string{"keyword", "a\"b", "", "名前", "\u0001<>&"}
	for range 500 {
		decs := make([]Decoration, r.IntN(20))
		for i := range decs {
			v := func() int { return vals[r.IntN(len(vals))] }
			decs[i] = Decoration{Range: rng(v(), v(), v(), v()), Style: styles[r.IntN(len(styles))]}
		}
		p := DecorationSetParams{Buffer: BufferID(r.Uint64()), Version: Version(r.Uint64() >> r.IntN(64)), Decorations: decs}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.JSONSize(); got != len(data) {
			t.Fatalf("JSONSize = %d, want %d for %s", got, len(data), data)
		}
	}
}

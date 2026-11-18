package protocol

import (
	"strings"
	"unicode/utf8"

	"github.com/piplupflipper/modal-editor/internal/ui"
)

// このファイルは、画面の内容を送る単位を決める（基本設計 1.2。10 章の未決の項目を #190 で決めた）。
//
// 単位は「internal/ui の格子の差分」である。
// server のイベントループは、従来どおり ui.Screen（格子の全体）を組み立てて Draw に入れる。
// 1 つのプロセスの中の接続（Pipe）は、その Draw を写さずにそのまま渡す。
// ソケットの接続（socket.go）は、送るときに screenEncoder が前回送った格子と比べて変わった行だけを載せ、
// 受けるときに screenDecoder が手元の格子に当てて ui.Screen に戻す。
// client の internal/term の Painter は、戻した格子と前回の格子のセルの差分を端末へ出す。
//
// この単位を、要件定義 8.1 の基準で計測する（docs/measurements/stage3-client-server-latency.md）。
// もう一方の案（表示のデータを送り、client で格子に描く）に替えるには、
// 格子への描画（internal/editor の render が呼ぶ ui.Renderer.Render）を client の側へ移す必要がある。

// Draw は、client の画面に出す内容である。
type Draw struct {
	// Screen は、画面の全体の内容とカーソルである。受け取った側は中身を変えない。
	Screen ui.Screen
}

func (Draw) serverMessage() {}

// wireDraw は、ソケットの上の Draw である。前回送った格子から変わった行だけを持つ。
type wireDraw struct {
	Width  int        `json:"width"`
	Height int        `json:"height"`
	Rows   []wireRow  `json:"rows,omitempty"`
	Cursor wireCursor `json:"cursor"`
}

// wireRow は、変わった 1 行である。行のセルを、属性が同じ連続の区間（wireRun）に分けて持つ。
type wireRow struct {
	Row  int       `json:"row"`
	Runs []wireRun `json:"runs"`
}

// wireRun は、属性が同じセルの連続である。
// 幅 1 で 1 文字のセル（ほとんどのセル）は、文字をつないだ Text で表す（1 文字が 1 セル）。
// それ以外のセル（全角文字、その続き、結合文字を含むもの）は Cells で 1 つずつ表す。1 つの区間はどちらか一方だけを持つ。
type wireRun struct {
	Attr  wireAttr   `json:"a,omitzero"`
	Text  string     `json:"t,omitempty"`
	Cells []wireCell `json:"c,omitempty"`
}

type wireCell struct {
	Text  string `json:"t"`
	Width int    `json:"w"`
}

type wireAttr struct {
	Fg        ui.Color `json:"fg,omitempty"`
	Bg        ui.Color `json:"bg,omitempty"`
	Sp        ui.Color `json:"sp,omitempty"`
	Bold      bool     `json:"b,omitempty"`
	Italic    bool     `json:"i,omitempty"`
	Underline bool     `json:"u,omitempty"`
	Undercurl bool     `json:"uc,omitempty"`
	Reverse   bool     `json:"r,omitempty"`
}

type wireCursor struct {
	Row     int            `json:"row"`
	Col     int            `json:"col"`
	Shape   ui.CursorShape `json:"shape"`
	Visible bool           `json:"visible"`
}

// screenEncoder は、server の側で、画面をソケットに載せる wireDraw にする。接続ごとに 1 つ持つ。ゼロ値で使える。
// 送った格子を覚え、次は変わった行だけを載せる。格子は渡した後に変えない約束（editor.Terminal.Draw）なので、写さずに覚える。
type screenEncoder struct {
	prev *ui.Grid
}

// encode は、s を wireDraw にする。最初と、大きさが変わったときは、すべての行を載せる。
func (e *screenEncoder) encode(s ui.Screen) wireDraw {
	g := s.Grid
	if g == nil {
		g = ui.NewGrid(0, 0)
	}
	full := e.prev == nil || e.prev.Width() != g.Width() || e.prev.Height() != g.Height()
	d := wireDraw{
		Width:  g.Width(),
		Height: g.Height(),
		Cursor: wireCursor{Row: s.Cursor.Row, Col: s.Cursor.Col, Shape: s.Cursor.Shape, Visible: s.Cursor.Visible},
	}
	for r := range g.Height() {
		if !full && sameRow(e.prev, g, r) {
			continue
		}
		d.Rows = append(d.Rows, wireRow{Row: r, Runs: encodeRow(g.Row(r))})
	}
	e.prev = g
	return d
}

func sameRow(a, b *ui.Grid, r int) bool {
	for c := range b.Width() {
		if a.At(r, c) != b.At(r, c) {
			return false
		}
	}
	return true
}

// simpleCell は、セルを wireRun の Text の 1 文字で表せるかを返す。
func simpleCell(c ui.Cell) bool {
	return c.Width == 1 && utf8.RuneCountInString(c.Text) == 1 && utf8.ValidString(c.Text)
}

func encodeRow(cells []ui.Cell) []wireRun {
	var runs []wireRun
	for i := 0; i < len(cells); {
		a, simple := cells[i].Attr, simpleCell(cells[i])
		run := wireRun{Attr: toWireAttr(a)}
		var text strings.Builder
		j := i
		for ; j < len(cells) && cells[j].Attr == a && simpleCell(cells[j]) == simple; j++ {
			if simple {
				text.WriteString(cells[j].Text)
			} else {
				run.Cells = append(run.Cells, wireCell{Text: cells[j].Text, Width: cells[j].Width})
			}
		}
		run.Text = text.String()
		runs = append(runs, run)
		i = j
	}
	return runs
}

// screenDecoder は、client の側で、届いた wireDraw を ui.Screen に戻す。接続ごとに 1 つ持つ。ゼロ値で使える。
type screenDecoder struct {
	prev *ui.Grid
}

// decode は、d を手元の格子に当てて、新しい格子の ui.Screen を返す。前に返した格子は変えない。
// 大きさが前回と違えば、載っていない行は空白にする（server はそのときすべての行を載せる）。
func (dec *screenDecoder) decode(d wireDraw) ui.Screen {
	g := ui.NewGrid(d.Width, d.Height)
	if p := dec.prev; p != nil && p.Width() == g.Width() && p.Height() == g.Height() {
		for r := range g.Height() {
			g.SetRow(r, p.Row(r))
		}
	}
	for _, row := range d.Rows {
		g.SetRow(row.Row, decodeRow(row.Runs, g.Width()))
	}
	dec.prev = g
	c := d.Cursor
	return ui.Screen{Grid: g, Cursor: ui.Cursor{Row: c.Row, Col: c.Col, Shape: c.Shape, Visible: c.Visible}}
}

func decodeRow(runs []wireRun, width int) []ui.Cell {
	cells := make([]ui.Cell, 0, width)
	for _, run := range runs {
		a := fromWireAttr(run.Attr)
		for _, r := range run.Text {
			cells = append(cells, ui.Cell{Text: string(r), Width: 1, Attr: a})
		}
		for _, c := range run.Cells {
			cells = append(cells, ui.Cell{Text: c.Text, Width: c.Width, Attr: a})
		}
	}
	return cells
}

func toWireAttr(a ui.Attr) wireAttr {
	return wireAttr{
		Fg: a.Fg, Bg: a.Bg, Sp: a.Sp,
		Bold: a.Bold, Italic: a.Italic, Underline: a.Underline, Undercurl: a.Undercurl, Reverse: a.Reverse,
	}
}

func fromWireAttr(a wireAttr) ui.Attr {
	return ui.Attr{
		Fg: a.Fg, Bg: a.Bg, Sp: a.Sp,
		Bold: a.Bold, Italic: a.Italic, Underline: a.Underline, Undercurl: a.Undercurl, Reverse: a.Reverse,
	}
}

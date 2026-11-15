package editor

import (
	"testing"
	"time"

	"github.com/piplupflipper/modal-editor/ext"
	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/ui"
)

// decoEditor は、イベントループを動かさずに、構文の色の表の作り直しを試すためのエディタである。
// 表を作り終えた知らせは、テストが e.decoDone から受け取って decorationsBuilt に渡す。
func decoEditor(t *testing.T) (*Editor, *bufferState) {
	t.Helper()
	bs := &bufferState{id: 1, buf: buffer.New("a\nb\nc\n"), decorations: map[string]decorationSet{}}
	e := &Editor{bufs: []*bufferState{bs}, decoDone: make(chan decoBuilt), stop: make(chan struct{})}
	t.Cleanup(func() { close(e.stop) })
	return e, bs
}

func decoList(line int) []ext.Decoration {
	return []ext.Decoration{{Range: ext.Range{Start: ext.Position{Line: line}, End: ext.Position{Line: line, Byte: 1}}, Style: "keyword"}}
}

// built は、作り終えた表を 1 つ受け取る。
func built(t *testing.T, e *Editor) decoBuilt {
	t.Helper()
	select {
	case r := <-e.decoDone:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("表が作り終わらない")
		return decoBuilt{}
	}
}

// noBuild は、作っている表が無いことを確かめる。
func noBuild(t *testing.T, e *Editor, bs *bufferState) {
	t.Helper()
	if bs.decoBuilding {
		t.Fatal("表を作っている")
	}
	select {
	case r := <-e.decoDone:
		t.Fatalf("余分な表が届いた: %+v", r.src)
	case <-time.After(20 * time.Millisecond):
	}
}

func (e *Editor) setDecos(extID string, decs []ext.Decoration) {
	e.setDecorations(extID, ext.DecorationSetParams{Buffer: 1, Version: ext.Version(e.bufs[0].buf.Version()), Decorations: decs})
}

// 一覧が届くと、表はイベントループの外で作られ、作り終えたものを受け取って描画に使う（#74）。
// 作っている間に届いた一覧は、作り終えた後にもう一度作って反映する。その間は、届いた順に新しい方の表で描く。
func TestDecorationsRebuiltOffLoop(t *testing.T) {
	e, bs := decoEditor(t)
	a, b, c := decoList(0), decoList(1), decoList(2)
	e.setDecos("x", a)
	if bs.decoView != nil || !bs.decoBuilding {
		t.Fatal("一覧が届いた時点で表を作った")
	}
	e.setDecos("x", b) // 作っている間に届いた。もう 1 つは作り始めない
	e.setDecos("x", c)
	r := built(t, e)
	if !sameSources(r.src, []decoSource{{"x", a}}) {
		t.Fatalf("作った元 = %+v", r.src)
	}
	e.decorationsBuilt(r)
	// a の表は今の一覧（c）のものではないが、前の表（無し）より新しいので描画に使い、c で作り直す。
	if bs.decoView != r.view || !bs.decoBuilding {
		t.Fatalf("途中の表を使わないか、作り直さない（view=%p, building=%v）", bs.decoView, bs.decoBuilding)
	}
	r = built(t, e)
	if !sameSources(r.src, []decoSource{{"x", c}}) {
		t.Fatalf("作り直した元 = %+v", r.src)
	}
	e.decorationsBuilt(r)
	if bs.decoView != r.view {
		t.Fatal("作り直した表を使わない")
	}
	noBuild(t, e, bs)
}

// 色を消した拡張機能の一覧を含む表と、読み込み直す前のバッファの表は、作り終えても描画に使わない。
func TestDecorationsStaleBuildDropped(t *testing.T) {
	t.Run("一覧が空になった", func(t *testing.T) {
		e, bs := decoEditor(t)
		e.setDecos("x", decoList(0))
		e.setDecos("x", nil)
		if bs.decoView != nil {
			t.Fatal("一覧が空になっても色が残る")
		}
		e.decorationsBuilt(built(t, e))
		if bs.decoView != nil {
			t.Fatal("消した一覧の表を使った")
		}
		noBuild(t, e, bs)
	})
	t.Run("一部の拡張機能が消えた", func(t *testing.T) {
		e, bs := decoEditor(t)
		e.setDecos("x", decoList(0))
		y := decoList(1)
		e.setDecos("y", y)
		e.clearExtension("x")
		e.decorationsBuilt(built(t, e))
		if bs.decoView != nil {
			t.Fatal("消した拡張機能の表を使った")
		}
		r := built(t, e)
		if !sameSources(r.src, []decoSource{{"y", y}}) {
			t.Fatalf("作り直した元 = %+v", r.src)
		}
		e.decorationsBuilt(r)
		if bs.decoView != r.view {
			t.Fatal("作り直した表を使わない")
		}
	})
	t.Run("読み込み直した", func(t *testing.T) {
		e, bs := decoEditor(t)
		e.setDecos("x", decoList(0))
		// save.go の読み込み直しと同じく、バッファを替えて一覧を捨てた後に、新しい一覧が届く。
		bs.buf = buffer.New("d\n")
		bs.decorations = map[string]decorationSet{}
		bs.decoView = nil
		e.setDecos("x", decoList(0))
		e.decorationsBuilt(built(t, e))
		if bs.decoView != nil {
			t.Fatal("読み込み直す前の表を使った")
		}
		e.decorationsBuilt(built(t, e))
		if bs.decoView == nil {
			t.Fatal("読み込み直した後の表を使わない")
		}
	})
}

// イベントループは、作り終えた表を受け取って描く。
//
// req: FR-35
func TestDecorationsDrawnAfterBuild(t *testing.T) {
	h := start(t, Config{})
	h.keys("iab<CR>cd<Esc>")
	h.settle()
	var bs *bufferState
	h.e.do(func() {
		bs = h.e.cur
		h.e.setDecorations("x", ext.DecorationSetParams{Buffer: bs.id, Version: ext.Version(bs.buf.Version()), Decorations: decoList(1)})
	})
	h.eventually("表を受け取る", func() bool { return bs.decoView != nil && !bs.decoBuilding })
	var f *ui.Frame
	h.e.do(func() { f = h.e.frame() })
	if f.Decorations != bs.decoView {
		t.Error("描画に表を渡していない")
	}
}

package exthost

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/internal/buffer"
)

// 不正な UTF-8 を含むバッファを拡張機能へ送らない取り決め（基本設計 4.1、5.3。#62）を確かめる。
// JSON の文字列は不正なバイトを表せず、encoding/json は U+FFFD に置き換えるので、送ると本体と複製で行頭からのバイト数がずれる。

// bufferMessages は、拡張機能の記録から、バッファの複製に関わるメッセージだけを取り出す。
func bufferMessages(log []string) []string {
	var msgs []string
	for _, l := range log {
		for _, p := range []string{"open:", "change:", "close:", "resync:", "bad-change:", "notification:" + ext.MethodBufferDidSave} {
			if strings.HasPrefix(l, p) {
				msgs = append(msgs, l)
				break
			}
		}
	}
	return msgs
}

// runOf は、拡張機能 id と、その稼働中のプロセスを返す。
func runOf(h *Host, id string) (*extension, *run) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.exts[id]
	return e, e.run
}

// JSON で送ると、不正なバイトが U+FFFD に置き換わり、バイト数が変わる。この取り決めが要る理由を固定しておく。
func TestJSONReplacesInvalidUTF8(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeNormal, "")}, nil, true)
	hs.h.Activate(ext.ActivationTextObject, "x.obj")
	hs.waitState("x", StateRunning)
	_, r := runOf(hs.h, "x")
	if err := r.conn.Notify(ext.MethodBufferOpen, ext.BufferOpenParams{Buffer: 1, Text: "a\xffb"}); err != nil {
		t.Fatal(err)
	}
	if got := hs.fakeState().Buffers[1].Text; got == "a\xffb" || len(got) == len("a\xffb") {
		t.Fatalf("replica = %q; JSON is expected to replace the invalid byte", got)
	}
}

// 不正なバイトを含む間は buffer/open も buffer/change も送らず、入ったら buffer/close、無くなったら buffer/open で全文を送る。
//
// req: FR-16
func TestInvalidUTF8NotSent(t *testing.T) {
	spec := fakeSpec(t, "x", modeNormal, "")
	spec.Manifest.SaveHook = true
	hs := newHarness(t, []Spec{spec}, nil, true)

	// 起動前に開いた、不正なバイトを含むバッファは、稼働中になっても送らない。
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 5, Version: 0, Text: "x\n\xff"}); err != nil {
		t.Fatal(err)
	}
	hs.h.Activate(ext.ActivationTextObject, "x.obj")
	hs.waitState("x", StateRunning)

	pos := func(l, by int) buffer.Position { return buffer.Position{Line: l, Byte: by} }
	ins := func(at buffer.Position, text string) buffer.Edit {
		return buffer.Edit{Range: buffer.Range{Start: at, End: at}, Text: text}
	}
	del := func(s, e buffer.Position) buffer.Edit { return buffer.Edit{Range: buffer.Range{Start: s, End: e}} }
	b := buffer.New("abc\ndef")
	apply := func(edits ...buffer.Edit) {
		t.Helper()
		c, err := b.Apply(buffer.Transaction{BaseVersion: b.Version(), Edits: edits})
		if err != nil {
			t.Fatal(err)
		}
		if err := hs.h.ChangeBuffer(1, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := hs.h.OpenBuffer(snapshot(1, "", b)); err != nil {
		t.Fatal(err)
	}
	apply(ins(pos(0, 1), "\xff"))    // v1: 不正なバイトが入った → close
	apply(ins(pos(1, 0), "\xe3"))    // v2: 不正なままの変更 → 送らない
	apply(del(pos(1, 0), pos(1, 1))) // v3: 1 つ消してもまだ不正 → 送らない

	// 送っていない間の、パスと finalNewline の変更、保存は送らない。
	if err := hs.h.UpdateBuffer(1, "/w/a.txt", "text"); err != nil {
		t.Fatal(err)
	}
	if err := hs.h.SetFinalNewline(1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.h.SendWillSave("x", ext.BufferWillSaveParams{Buffer: 1, Version: ext.Version(b.Version())}, SendOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("SendWillSave on withheld buffer = %v, want ErrUnavailable", err)
	}
	if err := hs.h.DidSave(ext.BufferDidSaveParams{Buffer: 1, Version: ext.Version(b.Version()), Path: "/w/a.txt"}); err != nil {
		t.Fatal(err)
	}
	if s := hs.fakeState(); len(s.Buffers) != 0 {
		t.Fatalf("extension holds %v while every buffer contains invalid UTF-8", s.Buffers)
	}

	apply(del(pos(0, 1), pos(0, 2))) // v4: 不正なバイトが無くなった → 全文
	// v5: 途中で不正なバイトを入れて、同じトランザクションの中で消す。前後とも正しいが、途中の Text が JSON で置き換わるので全文
	apply(ins(pos(0, 0), "\xff\xfe"), del(pos(0, 0), pos(0, 2)), ins(pos(0, 0), "é"))
	apply(ins(pos(1, 3), "!")) // v6: 正しいままの変更 → 差分

	// 送っていないバッファは、閉じても buffer/close を送らない。
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 2, Text: "\x80"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.h.CloseBuffer(2); err != nil {
		t.Fatal(err)
	}

	s := hs.fakeState()
	want := map[ext.BufferID]fakeBuffer{1: {Version: ext.Version(b.Version()), Text: b.Text()}}
	if fmt.Sprint(s.Buffers) != fmt.Sprint(want) {
		t.Fatalf("extension buffers = %v, want %v", s.Buffers, want)
	}
	if fn := s.FinalNewline[1]; fn == nil || *fn {
		t.Errorf("finalNewline = %v, want false (changed while withheld)", fn)
	}
	wantMsgs := []string{"open:1:0", "close:1", "open:1:4", "open:1:5", "change:1:5->6"}
	if msgs := bufferMessages(s.Log); !slices.Equal(msgs, wantMsgs) {
		t.Fatalf("buffer messages = %v, want %v", msgs, wantMsgs)
	}
}

// 送っていないバッファへの buffer/resync（buffer/close と行き違ったもの）には応じない。
//
// req: FR-16
func TestInvalidUTF8IgnoresResync(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeNormal, "")}, nil, true)
	if err := hs.h.OpenBuffer(BufferSnapshot{ID: 3, Text: "\xff"}); err != nil {
		t.Fatal(err)
	}
	hs.h.Activate(ext.ActivationTextObject, "x.obj")
	hs.waitState("x", StateRunning)
	e, r := runOf(hs.h, "x")
	hs.h.resync(e, r, ext.BufferResyncParams{Buffer: 3})
	if s := hs.fakeState(); len(s.Buffers) != 0 || len(bufferMessages(s.Log)) != 0 {
		t.Fatalf("extension state after resync of withheld buffer: %v %v", s.Buffers, s.Log)
	}
}

// 不正なバイトを混ぜた無作為の編集とアンドゥの各段階で、拡張機能の複製は、
// 本体のバッファが正しい UTF-8 なら同じ内容とバージョン番号を持ち、そうでなければ存在しない。
//
// req: FR-16
func TestInvalidUTF8RandomEdits(t *testing.T) {
	hs := newHarness(t, []Spec{fakeSpec(t, "x", modeNormal, "")}, nil, true)
	hs.h.Activate(ext.ActivationTextObject, "x.obj")
	hs.waitState("x", StateRunning)

	rng := rand.New(rand.NewPCG(62, 1)) //nolint:gosec // 再現できる無作為の編集が要る。暗号の用途ではない。
	texts := []string{"", "a", "é", "\n", "日本\n", "\xff", "\xe3\x81", "\x80x", "\n\xfe\n"}
	b := buffer.New("héllo\nwörld")
	if err := hs.h.OpenBuffer(snapshot(1, "", b)); err != nil {
		t.Fatal(err)
	}
	randPos := func() buffer.Position {
		l := rng.IntN(b.LineCount())
		return buffer.Position{Line: l, Byte: rng.IntN(len(b.Line(l)) + 1)}
	}
	withheldSteps, sentSteps := 0, 0
	for step := range 300 {
		var c buffer.Change
		if rng.IntN(5) == 0 && b.CanUndo() {
			var ok bool
			if c, ok = b.Undo(); !ok {
				t.Fatal("Undo failed")
			}
		} else {
			var edits []buffer.Edit
			for range 1 + rng.IntN(2) {
				s, e := randPos(), randPos()
				if e.Line < s.Line || (e.Line == s.Line && e.Byte < s.Byte) {
					s, e = e, s
				}
				edits = append(edits, buffer.Edit{Range: buffer.Range{Start: s, End: e}, Text: texts[rng.IntN(len(texts))]})
			}
			var err error
			// 位置が文字の途中を指すなどで本体が受け付けない編集は飛ばす。2 つ目の編集の位置は 1 つ目の前のテキストで選ぶので、外れることもある。
			if c, err = b.Apply(buffer.Transaction{BaseVersion: b.Version(), Edits: edits}); err != nil {
				continue
			}
		}
		if err := hs.h.ChangeBuffer(1, c); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		s := hs.fakeState()
		got, held := s.Buffers[1]
		if text := b.Text(); utf8.ValidString(text) {
			sentSteps++
			if !held || got.Text != text || got.Version != ext.Version(b.Version()) {
				t.Fatalf("step %d: replica = %+v (held %v), want %q at version %d", step, got, held, text, b.Version())
			}
		} else {
			withheldSteps++
			if held {
				t.Fatalf("step %d: extension holds %+v while the buffer %q contains invalid UTF-8", step, got, text)
			}
		}
		if bad := slices.ContainsFunc(s.Log, func(l string) bool { return strings.HasPrefix(l, "resync:") || strings.HasPrefix(l, "bad-change:") }); bad {
			t.Fatalf("step %d: extension log %v", step, s.Log)
		}
	}
	// 両方の状態を十分に通ったことを確かめる（無作為の選び方を変えて片方に偏らないように）。
	if withheldSteps < 20 || sentSteps < 20 {
		t.Fatalf("steps withheld %d, sent %d; both should be exercised", withheldSteps, sentSteps)
	}
}

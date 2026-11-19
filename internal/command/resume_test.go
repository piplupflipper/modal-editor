package command

import (
	"errors"
	"testing"

	"github.com/piplupflipper/modal-editor/internal/buffer"
	"github.com/piplupflipper/modal-editor/internal/command/target"
	"github.com/piplupflipper/modal-editor/internal/editcmd"
	"github.com/piplupflipper/modal-editor/internal/input"
)

// pendingExt は、再生中の問い合わせのうち、最初の allow 回だけ inner の結果を返し、それより後は ErrQueryPending を返す
// （結果を待たずに戻る internal/editor の実装の代役。allow 回は、届いた結果を返す実行し直しに当たる）。
type pendingExt struct {
	inner Extensions
	allow int
}

func (p *pendingExt) pass(info QueryInfo) bool {
	if !info.Replaying {
		return true
	}
	if p.allow > 0 {
		p.allow--
		return true
	}
	return false
}

func (p *pendingExt) Motion(ref editcmd.Ref, b *buffer.Buffer, req target.Request, info QueryInfo) (target.Result, error) {
	if !p.pass(info) {
		return target.Result{}, ErrQueryPending
	}
	return p.inner.Motion(ref, b, req, info)
}

func (p *pendingExt) TextObject(ref editcmd.Ref, b *buffer.Buffer, req target.Request, info QueryInfo) (target.Span, error) {
	if !p.pass(info) {
		return target.Span{}, ErrQueryPending
	}
	return p.inner.TextObject(ref, b, req, info)
}

func (p *pendingExt) Operator(ref editcmd.Ref, b *buffer.Buffer, q OperatorQuery, info QueryInfo) (buffer.Transaction, error) {
	if !p.pass(info) {
		return buffer.Transaction{}, ErrQueryPending
	}
	return p.inner.Operator(ref, b, q, info)
}

func (p *pendingExt) Action(ref editcmd.Ref, b *buffer.Buffer, q ActionQuery, info QueryInfo) (buffer.Transaction, error) {
	if !p.pass(info) {
		return buffer.Transaction{}, ErrQueryPending
	}
	return p.inner.Action(ref, b, q, info)
}

// resumeAll は、r が ErrQueryPending で止まっている間、届いた結果で ResumeReplay を呼び続け、最後の結果と呼んだ回数を返す。
// 同じ操作がまた止まった（拡張機能のテキストオブジェクトの後のオペレーター）ら、それまでの結果も返し直す（internal/editor の replayAnswers と同じ）。
func resumeAll(t *testing.T, s *State, inner Extensions, r Result) (Result, int) {
	t.Helper()
	n, allow := 0, 0
	var prev *Deferred
	for errors.Is(r.Err, ErrQueryPending) {
		if r.Deferred == nil {
			t.Fatalf("Deferred が無い: %+v", r)
		}
		if prev != nil && r.Deferred.Command == prev.Command && r.Deferred.Cursor == prev.Cursor && r.Deferred.Version == prev.Version {
			allow++
		} else {
			allow = 1
		}
		prev = r.Deferred
		r = s.ResumeReplay(*r.Deferred, &pendingExt{inner: inner, allow: allow}, r.Remaining, nil)
		n++
		if n > 20 {
			t.Fatal("再生が終わらない")
		}
	}
	return r, n
}

// 再生中の問い合わせが ErrQueryPending を返したら、State は操作と再生の残りを返して止まり、
// ResumeReplay が届いた結果で続ける。結果は、問い合わせのたびに待つ（同期の）実装と同じになる（FR-19）。
//
// req: FR-19
func TestResumeReplay(t *testing.T) {
	tests := []struct {
		name, text, record, play string
		resumes                  int
	}{
		// ysil) は、テキストオブジェクトとオペレーターの 2 つを問い合わせる。2 回の再生で 4 回止まる。
		{"2 つの問い合わせ", "ab\ncd\nef", "qaysil)jq", "2@a", 4},
		{"入れ子の再生", "ab\ncd\nef\ngh", "qagDjjq", "qb@aq@b", 2},
		{"再生のドットリピート", "ab\ncd\nef\ngh", "qagDjjq", "@a.", 2},
		{"ドットリピート", "ab cd", "ysiw)W", ".", 1},
	}
	for _, tt := range tests {
		// 問い合わせのたびに待つ実装での結果を、期待値にする。
		sync := New(newBuffer(tt.text), input.NewParser(extKeymap(t)))
		sync.Extensions = &fakeExt{}
		syncRes := feedKeys(sync, tt.record+tt.play)
		wantErr := syncRes[len(syncRes)-1].Err

		inner := &fakeExt{}
		s := New(newBuffer(tt.text), input.NewParser(extKeymap(t)))
		s.Extensions = inner
		for _, r := range feedKeys(s, tt.record) {
			if r.Err != nil {
				t.Fatalf("%s: 記録: err = %v", tt.name, r.Err)
			}
		}
		s.Extensions = &pendingExt{inner: inner}
		total := 0
		var last Result
		// 呼び出し側は、止まっている間は次のキーを渡さないので、1 個ずつ続け終えてから次を渡す。
		for _, k := range editcmd.MustParseKeys(tt.play) {
			r, n := resumeAll(t, s, inner, s.Feed(k))
			total += n
			last = r
		}
		if (last.Err == nil) != (wantErr == nil) {
			t.Errorf("%s: err = %v, want %v", tt.name, last.Err, wantErr)
		}
		if got, want := s.buf.Text(), sync.buf.Text(); got != want {
			t.Errorf("%s: text = %q, want %q", tt.name, got, want)
		}
		if got, want := s.Cursor(), sync.Cursor(); got != want {
			t.Errorf("%s: cursor = %v, want %v", tt.name, got, want)
		}
		if total != tt.resumes {
			t.Errorf("%s: ResumeReplay = %d 回, want %d", tt.name, total, tt.resumes)
		}
	}
}

// 結果を得られなかったか、待つ間にバッファが変わったら、ResumeReplay は操作を実行せずに再生を止める。
// 再生の残りの Dot の印は、ドットリピートの対象を再生した値にする（FR-57）。
func TestResumeReplayStops(t *testing.T) {
	for _, tt := range []struct {
		name    string
		between string
		err     error
		want    error
	}{
		{"失敗", "", errors.New("timeout"), nil},
		{"バッファが変わった", "x", nil, ErrStale},
	} {
		inner := &fakeExt{}
		s := New(newBuffer("ab\ncd"), input.NewParser(extKeymap(t)))
		s.Extensions = inner
		feedKeys(s, "qagDjxq")
		s.Extensions = &pendingExt{inner: inner}
		s.Feed(editcmd.Rune('@'))
		r := s.Feed(editcmd.Rune('a'))
		if !errors.Is(r.Err, ErrQueryPending) || r.Deferred == nil {
			t.Fatalf("%s: Result = %+v", tt.name, r)
		}
		s.Extensions = inner
		feedKeys(s, tt.between)
		before := s.buf.Text()
		r = s.ResumeReplay(*r.Deferred, &pendingExt{inner: inner, allow: 1}, r.Remaining, tt.err)
		want := tt.want
		if want == nil {
			want = tt.err
		}
		if !errors.Is(r.Err, want) {
			t.Errorf("%s: err = %v, want %v", tt.name, r.Err, want)
		}
		if got := s.buf.Text(); got != before {
			t.Errorf("%s: text = %q, want %q（続きを行わない）", tt.name, got, before)
		}
		if s.dot == nil || s.dot.Kind != editcmd.KindPlay || s.dot.Char != 'a' {
			t.Errorf("%s: dot = %+v, want @a", tt.name, s.dot)
		}
	}
}

package target

import (
	"github.com/piplupflipper/vi-imitation/internal/buffer"
	"github.com/piplupflipper/vi-imitation/internal/command/comment"
)

// commentedLines は、カーソルの行を含み、上下に続くコメントの行のまとまりを、行単位の範囲にする（オペレーターの後の gc。FR-169）。
// Neovim 0.12.5 の vim._comment.textobject と同じく、コメントの行かは comment.Parts.Checker で確かめ、空白だけの行はまとまりを切る。
// 回数は使わない（Neovim の 3dgc も dgc と同じ）。
//
// カーソルの行がコメントの行でなければ ErrNoTarget を返す。Neovim は何もせずにオペレーターを終える（ビープも鳴らさない）が、
// 本体では他のテキストオブジェクト（範囲の無い di( など）と同じく失敗にする。
// コメントの形が空か、前と後ろが空白だけ（"%s"）のときも ErrNoTarget を返す。Neovim はどの行もコメントの行として扱い、
// dgc でバッファの全体を消すが、設定していないことで全体を消さないように変えた（docs/vim-differences.md）。
//
// req: FR-169
func commentedLines(b *buffer.Buffer, req Request) (Span, error) {
	if err := validate(b, req); err != nil {
		return Span{}, err
	}
	var cs string
	if req.CommentString != nil {
		cs = req.CommentString(req.Cursor)
	}
	parts, err := comment.Parse(cs)
	if err != nil || !parts.HasMarker() {
		return Span{}, ErrNoTarget
	}
	commented := parts.Checker()
	line := req.Cursor.Line
	if !commented(b.Line(line)) {
		return Span{}, ErrNoTarget
	}
	from, to := line, line
	for from > 0 && commented(b.Line(from-1)) {
		from--
	}
	for to < b.LineCount()-1 && commented(b.Line(to+1)) {
		to++
	}
	return Span{Start: buffer.Position{Line: from}, End: buffer.Position{Line: to}, Kind: Linewise}, nil
}

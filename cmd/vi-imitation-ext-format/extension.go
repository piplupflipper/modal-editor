package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/piplupflipper/vi-imitation/ext"
	"github.com/piplupflipper/vi-imitation/ext/server"
)

// extension は、整形ツールの拡張機能である。
type extension struct {
	srv *server.Server
	// cfg は、initialize で読んだ設定である。initialize より後は変えないので、ハンドラーから起こした goroutine でも読める。
	cfg *config
	// ctx は、ハンドラーから起こした goroutine が本体へリクエスト（buffer/edit）を送るときの ctx である。
	// ハンドラーの ctx で送ると ErrEditInHandler になる（ext/server の Edit の doc）。
	ctx context.Context
	log *log.Logger
}

// newExtension は、ハンドラーを登録した拡張機能を作る。
func newExtension() *extension {
	x := &extension{srv: server.New(), cfg: &config{}, ctx: context.Background(), log: log.New(os.Stderr, "vi-imitation-ext-format: ", 0)}
	x.srv.OnInitialize(func(_ context.Context, e *server.InitializeEvent) error {
		cfg, err := parseSettings(e.Settings)
		if err != nil {
			return err
		}
		x.cfg = cfg
		return nil
	})
	x.srv.BufferWillSaveAsync(x.willSave)
	x.srv.ProviderAsync(ext.OpFormat, x.format)
	return x
}

// showMessage は、本体へ message/show を送る。送れなければログに書く。
func (x *extension) showMessage(level ext.MessageLevel, text string) {
	if err := x.srv.ShowMessage(level, text); err != nil {
		x.log.Printf("message/show: %v", err)
	}
}

// snapshot は、整形に要るバッファの中身である。ハンドラーが戻った後も読めるよう、ハンドラーの中で写す。
type snapshot struct {
	id           ext.BufferID
	version      ext.Version
	path         string
	fileType     string
	text         string
	finalNewline bool
}

func snapshotOf(b *server.Buffer) snapshot {
	return snapshot{id: b.ID(), version: b.Version(), path: b.Path(), fileType: b.FileType(), text: b.Text(), finalNewline: b.FinalNewline()}
}

// formatText は、s のファイルの種類に合う整形ツールを設定の順に走らせ、最後の整形ツールの出力をバッファの本文にして返す。
// どれかが失敗したら、そこで止めて誤りを返す（前の整形ツールの結果も使わない）。
// 合う整形ツールが無ければ、本文をそのまま返す。
//
// req: FR-175
func (x *extension) formatText(ctx context.Context, s snapshot) (string, error) {
	in := fileText(s.text, s.finalNewline)
	for _, f := range x.cfg.forFileType(s.fileType) {
		out, err := runFormatter(ctx, x.cfg, f, s.path, in)
		if err != nil {
			return "", err
		}
		in = out
	}
	return bufferText(in, s.finalNewline), nil
}

// report は、整形ツールの失敗 err を利用者へ知らせる。本体の取り消し（ctx の誤り）は知らせない。
func (x *extension) report(err error) {
	var fe *formatError
	if errors.As(err, &fe) {
		x.showMessage(ext.MessageWarning, fe.Error())
		return
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		x.log.Printf("format: %v", err)
	}
}

// willSave は、保存の直前の buffer/willSave に、整形ツールの結果のトランザクションを答える（基本設計 6.7、6.10）。
//
// 設定の formatOnSave が false か、バッファのファイルの種類に合う整形ツールが無ければ、編集無しを答える。
// 整形ツールは別の goroutine で走らせ、ext/server のメッセージを処理する goroutine を止めない（基本設計 2.2 の規約。ping に答えるため）。
// 整形ツールが失敗したら、編集無しを答えて保存を進めさせ、失敗を message/show で知らせる。
// 本体が期限（extensions.willSaveTimeoutMs）を超えて $/cancel を送ったら、整形ツールを止め、取り消しを応答する。
//
// req: FR-175
func (x *extension) willSave(ctx context.Context, b *server.Buffer, _ *ext.BufferWillSaveParams, reply func(ext.BufferWillSaveResult, error)) {
	if b == nil || !x.cfg.formatOnSave || len(x.cfg.forFileType(b.FileType())) == 0 {
		reply(ext.BufferWillSaveResult{}, nil)
		return
	}
	s := snapshotOf(b)
	go func() {
		text, err := x.formatText(ctx, s)
		switch {
		case ctx.Err() != nil:
			reply(ext.BufferWillSaveResult{}, ctx.Err())
		case err != nil:
			x.report(err)
			reply(ext.BufferWillSaveResult{}, nil)
		default:
			reply(ext.BufferWillSaveResult{Transaction: formatTransaction(s.text, text, s.version, nil)}, nil)
		}
	}()
}

// format は、本体の操作 format（gq{モーション} と :format）を処理する（基本設計 5.5、6.10）。
//
// 整形ツールに範囲を渡す手段は持たないので、いつも全体を整形ツールに渡す。
// 範囲（gq{モーション} の p.Range）があれば、整形の結果のうち範囲の行と重なる差分の塊だけを適用する（FR-176）。
// 範囲が無ければ（:format）全体に適用する。
// sync が false なら、受け付けたことだけを ok で応答し、結果を buffer/edit で送る。その間に利用者が編集していれば、
// 本体は適用しない（FR-15）ので、message/show でそのことを知らせる。
// sync が true（マクロとドットリピートの再生）なら、結果がそろってから、編集を応答の transaction で返し、失敗なら ok を false にする（FR-19）。
//
// req: FR-176
func (x *extension) format(ctx context.Context, b *server.Buffer, p *ext.ProviderInvokeParams, reply func(ext.ProviderInvokeResult, error)) {
	if p.Withheld || b == nil {
		// 不正な UTF-8 のために送られていないバッファ。理由は本体が知らせる（基本設計 5.3）。
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	if len(x.cfg.forFileType(b.FileType())) == 0 {
		x.showMessage(ext.MessageWarning, fmt.Sprintf("ファイルの種類 %s の整形ツールが設定に無い", b.FileType()))
		reply(ext.ProviderInvokeResult{OK: false}, nil)
		return
	}
	s := snapshotOf(b)
	var rg *lineRange
	if p.Range != nil {
		r := rangeLines(*p.Range)
		rg = &r
	}
	if p.Sync {
		go func() {
			text, err := x.formatText(ctx, s)
			if err != nil {
				x.report(err)
				reply(ext.ProviderInvokeResult{OK: false}, nil)
				return
			}
			reply(ext.ProviderInvokeResult{OK: true, Transaction: formatTransaction(s.text, text, s.version, rg)}, nil)
		}()
		return
	}
	reply(ext.ProviderInvokeResult{OK: true}, nil)
	go func() {
		text, err := x.formatText(x.ctx, s)
		if err != nil {
			x.report(err)
			return
		}
		t := formatTransaction(s.text, text, s.version, rg)
		if t == nil {
			return
		}
		res, err := x.srv.Edit(x.ctx, ext.BufferEditParams{Buffer: s.id, Transaction: *t})
		switch {
		case err != nil:
			x.log.Printf("buffer/edit: %v", err)
			x.showMessage(ext.MessageError, fmt.Sprintf("整形を適用できない: %v", err))
		case !res.Applied:
			x.showMessage(ext.MessageWarning, "整形の間にバッファが変わったので、整形を適用しなかった")
		}
	}()
}

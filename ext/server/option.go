package server

import (
	"context"

	"github.com/piplupflipper/vi-imitation/ext"
)

// SetOptions は、本体へ option/set を送り、本体の設定の項目の値を変える（ext.MethodOptionSet の doc。#349。FR-190、FR-191）。
// p.Buffer を書けばそのバッファの範囲がバッファごとの項目を、省けば範囲がエディタ全体の項目を変える。
// 値は利用者が :set で変えた値には勝たない。表に無い名前や範囲の外の値があれば、本体はどの値も入れずに誤りで応答する。
// p が ext.OptionSetParams.Validate を通らなければ、送らずにその誤りを返す。
// Edit と同じく、ループの上から呼ぶと ErrEditInHandler を返す（buffer/open のフックからは goroutine を起こして呼ぶ）。
func (s *Server) SetOptions(ctx context.Context, p ext.OptionSetParams) error {
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := call[ext.OptionSetResult](ctx, s, ext.MethodOptionSet, p)
	return err
}

// GetOptions は、本体へ option/get を送り、設定の項目の今の値を読む（ext.MethodOptionGet の doc。#349。FR-192）。
// Edit と同じく、ループの上から呼ぶと ErrEditInHandler を返す。
func (s *Server) GetOptions(ctx context.Context, p ext.OptionGetParams) (ext.OptionGetResult, error) {
	return call[ext.OptionGetResult](ctx, s, ext.MethodOptionGet, p)
}

package config

import (
	"strconv"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// option は、最上位の項目 m が設定の表（defs。option.Defs）の項目かその入れ子の段なら読んで true を返す（FR-131。基本設計 7.3）。
// 表の項目でもその段でもなければ、何も読まずに false を返す（呼び出し側が未知の項目として知らせる）。
//
// 点で区切った項目の名前は、入れ子のキーとして書く（lineNumber.show は "lineNumber": { "show": true }）。
// 範囲がエディタ全体の項目だけを読み、値を項目の型（真偽、整数、文字列の JSON の値）で Config.Options に入れる。
// 範囲がバッファごとの項目は、初めの値がバッファを開くときに決まるので、書けば誤りとして知らせて使わない。
// この規則は項目の範囲で決め、項目の名前では分けない。
//
// req: FR-131
func (s *source) option(m member, prefix string, defs []option.Def, cfg *Config) bool {
	name := m.name
	if prefix != "" {
		name = prefix + "." + m.name
	}
	for _, d := range defs {
		if d.Name != name {
			continue
		}
		if d.Scope != option.ScopeEditor {
			s.report(m.nameOff, "%s はバッファごとの設定なので、設定ファイルには書けない", name)
			return true
		}
		if v, ok := s.optionValue(m.value, d); ok {
			if cfg.Options == nil {
				cfg.Options = map[string]option.Value{}
			}
			cfg.Options[name] = v
		}
		return true
	}
	if !hasOptionUnder(defs, name) {
		return false
	}
	obj, ok := s.object(m.value, name)
	if !ok {
		return true
	}
	for _, c := range s.members(obj, name) {
		if !s.option(c, name, defs, cfg) {
			s.report(c.nameOff, "未知の項目 %s を無視する", join(name, c.name))
		}
	}
	return true
}

// hasOptionUnder は、defs に、名前が prefix と点で始まる項目があるかを返す。
func hasOptionUnder(defs []option.Def, prefix string) bool {
	for _, d := range defs {
		if strings.HasPrefix(d.Name, prefix+".") {
			return true
		}
	}
	return false
}

// optionValue は、v を項目 d の型の値として読む。型が違えば誤りを記録して false を返す。
func (s *source) optionValue(v *hujson.Value, d option.Def) (option.Value, bool) {
	switch d.Type {
	case option.Bool:
		if b, ok := s.boolean(v, d.Name); ok {
			return option.BoolValue(b), true
		}
	case option.Int:
		if l, ok := v.Value.(hujson.Literal); ok && l.Kind() == '0' {
			if n, err := strconv.Atoi(string(l)); err == nil {
				return option.IntValue(n), true
			}
		}
		s.report(v.StartOffset, "%s は整数にする", d.Name)
	case option.String:
		if str, ok := s.str(v, d.Name); ok {
			return option.StringValue(str), true
		}
	}
	return option.Value{}, false
}

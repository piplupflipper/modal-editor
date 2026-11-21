package config

import (
	"errors"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/piplupflipper/vi-imitation/internal/option"
)

// option は、最上位の項目 m が設定の表（defs。option.Defs）の項目かその入れ子の段なら読んで true を返す（FR-131。基本設計 7.3）。
// 表の項目でもその段でもなければ、何も読まずに false を返す（呼び出し側が未知の項目として知らせる）。
//
// 点で区切った項目の名前は、入れ子のキーとして書く（lineNumber.show は "lineNumber": { "show": true }）。
// 値を項目の型（真偽、整数、文字列の JSON の値）で Config.Options に入れる。
// 範囲がエディタ全体の項目は、エディタの初めの値になる。範囲がバッファごとの項目は、バッファを開くときの初めの値になる（FR-163）。
// ただし、バッファを開くときにファイルから判定して決める項目（option.Def.Detected。filetype）は、書けば誤りとして知らせて使わない。
// この規則は項目の範囲と表の欄で決め、項目の名前では分けない。
//
// req: FR-131, FR-163
func (s *source) option(m member, prefix string, defs []option.Def, cfg *Config) bool {
	return s.optionInto(m, prefix, defs, &cfg.Options, false)
}

// optionInto は、option の読み方で、値を *dst に入れる。perType なら、ファイルの種類ごとの値（fileTypeOptions の中）として読み、
// 範囲がエディタ全体の項目も誤りとして知らせて使わない（バッファごとにしか値を持たないため）。
func (s *source) optionInto(m member, prefix string, defs []option.Def, dst *map[string]option.Value, perType bool) bool {
	name := m.name
	if prefix != "" {
		name = prefix + "." + m.name
	}
	for _, d := range defs {
		if d.Name != name {
			continue
		}
		switch {
		case d.Detected:
			s.report(m.nameOff, "%s はバッファを開くときに決まる設定なので、設定ファイルには書けない", name)
			return true
		case perType && d.Scope != option.ScopeBuffer:
			s.report(m.nameOff, "%s はエディタ全体の設定なので、ファイルの種類ごとには書けない", name)
			return true
		}
		if v, ok := s.optionValue(m.value, d); ok {
			if *dst == nil {
				*dst = map[string]option.Value{}
			}
			(*dst)[name] = v
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
		if !s.optionInto(c, name, defs, dst, perType) {
			s.report(c.nameOff, "未知の項目 %s を無視する", join(name, c.name))
		}
	}
	return true
}

// fileTypeOptions は、fileTypeOptions の値を読む（FR-163。基本設計 7.3）。
// キーはファイルの種類の名前（filetype の値。"go"）で、値は、設定の表の範囲がバッファごとの項目を、最上位と同じ入れ子の形で書いたオブジェクトである
// （"fileTypeOptions": { "go": { "indent": { "useSpaces": false } } }）。
// 範囲がエディタ全体の項目と、ファイルから判定して決める項目（filetype）は、誤りとして知らせて使わない。
// 値の検査は最上位と同じ（option.Def.Check）である。
//
// req: FR-163
func (s *source) fileTypeOptions(v *hujson.Value, defs []option.Def) map[string]map[string]option.Value {
	obj, ok := s.object(v, "fileTypeOptions")
	if !ok {
		return nil
	}
	var out map[string]map[string]option.Value
	for _, ft := range s.members(obj, "fileTypeOptions") {
		path := join("fileTypeOptions", ft.name)
		if ft.name == "" {
			s.report(ft.nameOff, "fileTypeOptions のファイルの種類の名前が空")
			continue
		}
		items, ok := s.object(ft.value, path)
		if !ok {
			continue
		}
		var vals map[string]option.Value
		for _, m := range s.members(items, path) {
			if !s.optionInto(m, "", defs, &vals, true) {
				s.report(m.nameOff, "未知の項目 %s を無視する", join(path, m.name))
			}
		}
		if vals == nil {
			continue
		}
		if out == nil {
			out = map[string]map[string]option.Value{}
		}
		out[ft.name] = vals
	}
	return out
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

// optionValue は、v を項目 d の型の値に変換し、コマンドラインから読む値と同じ検査（option.Def.Check）を通して返す。
// 変換できないか、検査で誤りなら、誤りを記録して false を返す（呼び出し側は値を入れないので、表の既定値を使う）。
// 整数の範囲（option.Def.Range）の外の値は、範囲を添えて知らせる（FR-136）。項目の名前では分けない。
//
// req: FR-131, FR-136
func (s *source) optionValue(v *hujson.Value, d option.Def) (option.Value, bool) {
	val, ok := s.convertOption(v, d)
	if !ok {
		return option.Value{}, false
	}
	if err := d.Check(val); err != nil {
		s.reportInvalid(v, d, val.String(), err)
		return option.Value{}, false
	}
	return val, true
}

// reportInvalid は、項目 d に入れられない値 text（検査の誤り err）を、v の位置の誤りとして記録する。
// 整数の範囲の外（option.ErrOutOfRange）なら、範囲を添える。
func (s *source) reportInvalid(v *hujson.Value, d option.Def, text string, err error) {
	if errors.Is(err, option.ErrOutOfRange) && d.Range != nil {
		s.report(v.StartOffset, "%s は %d から %d の整数にする: %s", d.Name, d.Range.Min, d.Range.Max, text)
		return
	}
	if errors.Is(err, option.ErrInvalidWords) && d.Words != nil {
		// req: FR-196
		s.report(v.StartOffset, "%s は %s の語をコロンとコンマで区切って書く（部分は %d 個まで）: %s",
			d.Name, strings.Join(d.Words.Words, "、"), d.Words.MaxParts, text)
		return
	}
	s.report(v.StartOffset, "%s の値が正しくない: %s", d.Name, text)
}

// convertOption は、JSON の値 v を項目 d の型の値に変換する（真偽、整数、文字列の JSON の値）。値の検査はしない。
// 型が違えば誤りを記録して false を返す。
// 整数はコマンドラインの値と同じ option.ParseInt で読み、int に収まらない数は範囲の外の誤りとして記録する（FR-136）。
func (s *source) convertOption(v *hujson.Value, d option.Def) (option.Value, bool) {
	switch d.Type {
	case option.Bool:
		if b, ok := s.boolean(v, d.Name); ok {
			return option.BoolValue(b), true
		}
	case option.Int:
		if l, ok := v.Value.(hujson.Literal); ok && l.Kind() == '0' {
			n, err := option.ParseInt(string(l))
			switch {
			case err == nil:
				return option.IntValue(n), true
			case errors.Is(err, option.ErrOutOfRange):
				s.reportInvalid(v, d, string(l), err)
				return option.Value{}, false
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

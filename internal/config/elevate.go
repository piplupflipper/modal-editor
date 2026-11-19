package config

import "github.com/tailscale/hujson"

// elevateCommand は、elevateCommand の値を読む（基本設計 4.7。FR-79。項目の名前は仮）。
//
// 値は空でない文字列の配列で、最初の要素がコマンド、残りがその引数である（["sudo"]、["doas"]、["sudo", "-k"]）。
// 本体は、この後に tee <パス>（と mkdir -p <ディレクトリ>）を付けて、シェルを通さずに起動する。
// 空の配列、文字列でない要素、空の文字列の要素があれば、項目全体を誤りとして記録し、nil（既定の ["sudo"]）を返す。
// 一部の要素だけを捨てると、利用者の意図と違うコマンドで権限を上げかねないためである。
//
// req: FR-79
func (s *source) elevateCommand(v *hujson.Value) []string {
	const path = "elevateCommand"
	arr, ok := s.array(v, path)
	if !ok {
		return nil
	}
	if len(arr.Elements) == 0 {
		s.report(v.StartOffset, "%s は空でない配列にする（既定の [\"sudo\"] を使う）", path)
		return nil
	}
	out := make([]string, 0, len(arr.Elements))
	for i := range arr.Elements {
		el := &arr.Elements[i]
		str, ok := s.str(el, path+" の要素")
		if !ok {
			return nil
		}
		if str == "" {
			s.report(el.StartOffset, "%s の要素が空の文字列（既定の [\"sudo\"] を使う）", path)
			return nil
		}
		out = append(out, str)
	}
	return out
}

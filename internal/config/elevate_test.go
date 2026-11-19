package config

import (
	"reflect"
	"testing"
)

// elevateCommand は、空でない文字列の配列として読み、誤りがあれば項目全体を捨てて既定（nil）にする（FR-79）。
//
// req: FR-79
func TestParseElevateCommand(t *testing.T) {
	tests := []struct {
		src     string
		want    []string
		wantErr bool
	}{
		{`{"elevateCommand": ["doas"]}`, []string{"doas"}, false},
		{`{"elevateCommand": ["sudo", "-k"]}`, []string{"sudo", "-k"}, false},
		{`{}`, nil, false},
		{`{"elevateCommand": []}`, nil, true},
		{`{"elevateCommand": "sudo"}`, nil, true},
		{`{"elevateCommand": ["sudo", 1]}`, nil, true},
		{`{"elevateCommand": ["sudo", ""]}`, nil, true},
	}
	for _, tt := range tests {
		cfg, errs := Parse("config.jsonc", []byte(tt.src))
		if !reflect.DeepEqual(cfg.ElevateCommand, tt.want) {
			t.Errorf("%s: ElevateCommand = %q, want %q", tt.src, cfg.ElevateCommand, tt.want)
		}
		if (len(errs) > 0) != tt.wantErr {
			t.Errorf("%s: errs = %v, want error %v", tt.src, errs, tt.wantErr)
		}
	}
}

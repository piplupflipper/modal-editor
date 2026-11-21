package ext

import (
	"encoding/json"
	"testing"
)

// option/set と option/get の params と result の JSON の形（基本設計 5.3）。
func TestOptionJSON(t *testing.T) {
	roundTrip(t, OptionSetParams{Buffer: 2, Options: map[string]json.RawMessage{"indent.width": json.RawMessage("2"), "tab.width": OptionUnset}},
		`{"buffer":2,"options":{"indent.width":2,"tab.width":null}}`)
	roundTrip(t, OptionSetParams{Options: map[string]json.RawMessage{"lineWrap.enabled": json.RawMessage("true")}},
		`{"options":{"lineWrap.enabled":true}}`)
	roundTrip(t, OptionGetParams{Buffer: 1, Names: []string{"indent.width"}}, `{"buffer":1,"names":["indent.width"]}`)
	roundTrip(t, OptionGetParams{}, `{}`)
	roundTrip(t, OptionGetResult{Values: map[string]json.RawMessage{"comment.string": json.RawMessage(`"# %s"`)}},
		`{"values":{"comment.string":"# %s"}}`)
	if err := (OptionSetParams{Buffer: 1}).Validate(); err == nil {
		t.Error("空の options を受け付けた")
	}
	if err := (OptionSetParams{Options: map[string]json.RawMessage{"x": OptionUnset}}).Validate(); err != nil {
		t.Errorf("Validate = %v", err)
	}
}

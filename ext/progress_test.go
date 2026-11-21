package ext

import (
	"encoding/json"
	"testing"
)

// progress/report の params の JSON の形と、本体が受け付けるかの検査（基本設計 5.3）。
func TestProgressReportParams(t *testing.T) {
	b, err := json.Marshal(ProgressReportParams{Token: "1", Kind: ProgressBegin, Title: "Loading", Percentage: Percent(0)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"token":"1","kind":"begin","title":"Loading","percentage":0}`; got != want {
		t.Errorf("params = %s, want %s", got, want)
	}
	var p ProgressReportParams
	if err := json.Unmarshal([]byte(`{"token":"t","kind":"report","message":"3/10"}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Percentage != nil || p.Message != "3/10" || p.Kind != ProgressReport {
		t.Errorf("Unmarshal = %+v", p)
	}
	for _, tc := range []struct {
		p  ProgressReportParams
		ok bool
	}{
		{ProgressReportParams{Token: "t", Kind: ProgressBegin}, true},
		{ProgressReportParams{Token: "t", Kind: ProgressReport}, true},
		{ProgressReportParams{Token: "t", Kind: ProgressEnd}, true},
		{ProgressReportParams{Kind: ProgressBegin}, false},
		{ProgressReportParams{Token: "t", Kind: "cancel"}, false},
		{ProgressReportParams{Token: "t"}, false},
	} {
		if err := tc.p.Validate(); (err == nil) != tc.ok {
			t.Errorf("Validate(%+v) = %v, want ok %v", tc.p, err, tc.ok)
		}
	}
}

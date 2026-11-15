package version

import "testing"

func TestString(t *testing.T) {
	// テストでは -ldflags を指定しないので、既定値が返る。
	if got, want := String(), "dev"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

package duration

import "testing"

func TestUnits(t *testing.T) {
	for s, want := range map[string]int64{"250ms": 250, "2s": 2000, "3m": 180000, "0s": 0, " 12s\n": 12000} {
		got, e := Parse(s)
		if e != nil || got != want {
			t.Errorf("Parse(%q)=%d,%v; want %d", s, got, e, want)
		}
	}
}
func TestInvalid(t *testing.T) {
	for _, s := range []string{"", "1", "-1s", "+1s", "1.5s", "1 s", "1S", "1h", "1s extra", "١s", "１ms", "999999999999999999999999m"} {
		if _, e := Parse(s); e == nil {
			t.Errorf("accepted %q", s)
		}
	}
}

package poker

import "testing"

// fib is the standard (non-extended) Fibonacci scale used across tests.
var fib = mustScale(TypeFibonacci, false, false)

func mustScale(t string, extended, zero bool) Scale {
	s, err := ScaleFor(t, extended, zero)
	if err != nil {
		panic(err)
	}
	return s
}

func TestScaleFor(t *testing.T) {
	tests := []struct {
		typ        string
		extended   bool
		zero       bool
		wantLabels []string
	}{
		{TypeFibonacci, false, false, []string{"1", "2", "3", "5", "8"}},
		{TypeFibonacci, true, false, []string{"1", "2", "3", "5", "8", "13", "21"}},
		{TypeFibonacci, false, true, []string{"0", "1", "2", "3", "5", "8"}},
		{TypeExponential, false, false, []string{"1", "2", "4", "8", "16"}},
		{TypeExponential, true, false, []string{"1", "2", "4", "8", "16", "32", "64"}},
		{TypeLinear, true, false, []string{"1", "2", "3", "4", "5", "6", "7"}},
		{TypeTShirt, false, false, []string{"XS", "S", "M", "L", "XL"}},
		{TypeTShirt, true, false, []string{"XS", "S", "M", "L", "XL", "XXL", "XXXL"}},
	}
	for _, tt := range tests {
		s, err := ScaleFor(tt.typ, tt.extended, tt.zero)
		if err != nil {
			t.Errorf("ScaleFor(%q,%v,%v): %v", tt.typ, tt.extended, tt.zero, err)
			continue
		}
		got := s.Labels()
		if len(got) != len(tt.wantLabels) {
			t.Errorf("%q labels = %v, want %v", tt.typ, got, tt.wantLabels)
			continue
		}
		for i := range got {
			if got[i] != tt.wantLabels[i] {
				t.Errorf("%q labels = %v, want %v", tt.typ, got, tt.wantLabels)
				break
			}
		}
	}
}

func TestTShirtMapsToFibonacci(t *testing.T) {
	s := mustScale(TypeTShirt, false, false)
	want := map[string]float64{"XS": 1, "S": 2, "M": 3, "L": 5, "XL": 8}
	for _, p := range s {
		if want[p.Label] != p.Value {
			t.Errorf("%s = %v, want %v", p.Label, p.Value, want[p.Label])
		}
	}
}

func TestScaleForDisabled(t *testing.T) {
	for _, typ := range []string{"", TypeNotUsed, "bogus"} {
		if _, err := ScaleFor(typ, false, false); err == nil {
			t.Errorf("ScaleFor(%q) expected error", typ)
		}
	}
}

func TestConsensus(t *testing.T) {
	tests := []struct {
		name      string
		votes     map[string]string
		wantLabel string
		wantOK    bool
	}{
		{"no votes", map[string]string{}, "", false},
		{"votes off-scale ignored", map[string]string{"u1": "99"}, "", false},
		{"single vote", map[string]string{"u1": "8"}, "8", true},
		{"clear majority", map[string]string{"u1": "5", "u2": "5", "u3": "8"}, "5", true},
		// every vote distinct → three-way tie on count → highest value wins
		{"all distinct votes tie to highest", map[string]string{"u1": "1", "u2": "5", "u3": "13"}, "13", true},
		// 2x"2" and 2x"3" tie on count 2 → highest value wins
		{"tied counts break upward", map[string]string{"u1": "2", "u2": "2", "u3": "3", "u4": "3"}, "3", true},
		{"tie between two single votes rounds up", map[string]string{"u1": "2", "u2": "3"}, "3", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// use extended fib so "13" is on-scale
			s := mustScale(TypeFibonacci, true, false)
			p, ok := s.Consensus(tt.votes)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && p.Label != tt.wantLabel {
				t.Errorf("label = %q, want %q", p.Label, tt.wantLabel)
			}
		})
	}
}

func TestContains(t *testing.T) {
	if !fib.Contains("5") || fib.Contains("4") || fib.Contains("?") {
		t.Errorf("Contains wrong for fib scale")
	}
}

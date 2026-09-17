package poker

import "testing"

func TestContains(t *testing.T) {
	cases := map[string]bool{
		"1": true, "21": true, "?": true,
		"4": false, "": false, "13 ": false,
	}
	for label, want := range cases {
		if got := Fibonacci.Contains(label); got != want {
			t.Errorf("Contains(%q) = %v, want %v", label, got, want)
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
		{
			name:   "no votes",
			votes:  map[string]string{},
			wantOK: false,
		},
		{
			name:   "only unsure votes",
			votes:  map[string]string{"u1": "?", "u2": "?"},
			wantOK: false,
		},
		{
			name:      "single vote",
			votes:     map[string]string{"u1": "8"},
			wantLabel: "8",
			wantOK:    true,
		},
		{
			name:      "odd count exact median",
			votes:     map[string]string{"u1": "1", "u2": "5", "u3": "13"},
			wantLabel: "5",
			wantOK:    true,
		},
		{
			name:      "even count median snaps to nearest scale point",
			votes:     map[string]string{"u1": "3", "u2": "8"}, // median 5.5 -> nearest is 5
			wantLabel: "5",
			wantOK:    true,
		},
		{
			name:      "unsure votes ignored in median",
			votes:     map[string]string{"u1": "8", "u2": "?", "u3": "8"},
			wantLabel: "8",
			wantOK:    true,
		},
		{
			name:      "median between points rounds to nearer high",
			votes:     map[string]string{"u1": "8", "u2": "13"}, // median 10.5 -> nearest 13 (dist 2.5) vs 8 (2.5) tie -> lower 8
			wantLabel: "8",
			wantOK:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, _, ok := Fibonacci.Consensus(tt.votes)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}

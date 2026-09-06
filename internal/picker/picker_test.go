package picker

import "testing"

func TestMatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		filter string
		target string
		want   bool
	}{
		{"", "devbox/backend", true},
		{"backend", "devbox/backend", true},
		{"db/be", "devbox/backend", true},
		{"xyz", "devbox/backend", false},
		{"DEV", "devbox/backend", true},
	}
	for _, tt := range tests {
		t.Run(tt.filter+"/"+tt.target, func(t *testing.T) {
			t.Parallel()
			if got := match(tt.filter, tt.target); got != tt.want {
				t.Fatalf("match(%q, %q) = %v", tt.filter, tt.target, got)
			}
		})
	}
}

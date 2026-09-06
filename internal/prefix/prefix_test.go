package prefix

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want byte
	}{
		{"", 0},
		{"ctrl-space", 0},
		{"ctrl-g", 0x07},
		{"ctrl-a", 1},
		{"ctrl+b", 2},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseUnknown(t *testing.T) {
	t.Parallel()
	if _, err := Parse("alt-x"); err == nil {
		t.Fatal("expected error")
	}
}

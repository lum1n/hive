package workspace

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in      string
		host    string
		session string
		ok      bool
	}{
		{"tmux://devbox/backend", "devbox", "backend", true},
		{"devbox/backend", "devbox", "backend", true},
		{"tmux://gpu01/train/job", "gpu01", "train/job", true},
		{"", "", "", false},
		{"backend", "", "", false},
		{"http://devbox/backend", "", "", false},
		{"bad host/backend", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			id, err := Parse(tt.in)
			if tt.ok && err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("Parse(%q): want error", tt.in)
			}
			if tt.ok && (id.Host != tt.host || id.Session != tt.session) {
				t.Fatalf("Parse(%q) = %+v", tt.in, id)
			}
		})
	}
}

func TestDisplay(t *testing.T) {
	t.Parallel()
	id := ID{Host: "devbox", Session: "backend"}
	if id.String() != "tmux://devbox/backend" {
		t.Fatalf("string: %s", id.String())
	}
	if id.Display() != "devbox/backend" {
		t.Fatalf("display: %s", id.Display())
	}
}

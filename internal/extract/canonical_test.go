package extract

import "testing"

func TestCanonicalStripsSyntheticNames(t *testing.T) {
	tests := []struct{ in, want string }{
		{"func(ctx context.Context) (#rv1 int, #rv2 error)", "func(ctx context.Context) (int, error)"},
		{"func(#p0 int, b string)", "func(int, b string)"},
		{"func(f func(#p0 int) #rv1 bool)", "func(f func(int) bool)"},
		{"func() int", "func() int"},
		{"map[string]struct{}", "map[string]struct{}"},
	}
	for _, tt := range tests {
		if got := canonical(tt.in); got != tt.want {
			t.Errorf("canonical(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

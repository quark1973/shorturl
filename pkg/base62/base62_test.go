package base62

import "testing"

func TestEncode(t *testing.T) {
	tests := []struct {
		name string
		id   uint64
		want string
	}{
		{name: "zero", id: 0, want: "0"},
		{name: "single digit", id: 61, want: "Z"},
		{name: "carry", id: 62, want: "10"},
		{name: "short code", id: 3844, want: "100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Encode(tt.id); got != tt.want {
				t.Fatalf("Encode(%d) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

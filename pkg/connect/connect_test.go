package connect_test

import(
	"shorturl/pkg/connect"
	"testing"
)

func TestGet(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		url  string
		want bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := connect.Get(tt.url)
			// TODO: update the condition below to compare got with tt.want.
			if true {
				t.Errorf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

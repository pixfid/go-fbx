package pathutil

import "testing"

func TestNormalize(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "books/a.fb2", want: "books/a.fb2"},
		{in: "books\\a.fb2", want: "books/a.fb2"},
		{in: "./books/a.fb2", want: "books/a.fb2"},
		{in: "", wantErr: true},
		{in: "/abs", wantErr: true},
		{in: "a/../b", want: "b"},
		{in: "../a", wantErr: true},
	}
	for _, tc := range tests {
		got, err := Normalize(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("Normalize(%q): expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("Normalize(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("Normalize(%q): got %q want %q", tc.in, got, tc.want)
		}
	}
}

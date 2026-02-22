package format

import "testing"

func TestFNV1a64KnownVector(t *testing.T) {
	// FNV-1a 64 for "hello"
	const want uint64 = 0xa430d84680aabd0b
	if got := FNV1a64("hello"); got != want {
		t.Fatalf("got %#x want %#x", got, want)
	}
}

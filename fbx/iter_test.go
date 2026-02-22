package fbx

import (
	"errors"
	"testing"
)

func TestSliceIteratorBasic(t *testing.T) {
	it := newSliceIterator([]int{10, 20}, nil)
	if !it.Next() || it.Value() != 10 {
		t.Fatalf("unexpected first value")
	}
	if !it.Next() || it.Value() != 20 {
		t.Fatalf("unexpected second value")
	}
	if it.Next() {
		t.Fatalf("iterator should be exhausted")
	}
	if it.Value() != 0 {
		t.Fatalf("expected zero value after exhaustion")
	}
}

func TestSliceIteratorErrorStopsIteration(t *testing.T) {
	err := errors.New("boom")
	it := newSliceIterator([]int{1}, err)
	if it.Next() {
		t.Fatalf("expected Next=false when iterator has error")
	}
	if !errors.Is(it.Err(), err) {
		t.Fatalf("unexpected iterator error: %v", it.Err())
	}
}

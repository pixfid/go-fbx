package fbx

type Iterator[T any] interface {
	Next() bool
	Value() T
	Err() error
}

type sliceIterator[T any] struct {
	values []T
	idx    int
	err    error
}

func newSliceIterator[T any](values []T, err error) *sliceIterator[T] {
	return &sliceIterator[T]{values: values, idx: -1, err: err}
}

func (it *sliceIterator[T]) Next() bool {
	if it.err != nil {
		return false
	}
	it.idx++
	return it.idx < len(it.values)
}

func (it *sliceIterator[T]) Value() T {
	if it.idx < 0 || it.idx >= len(it.values) {
		var zero T
		return zero
	}
	return it.values[it.idx]
}

func (it *sliceIterator[T]) Err() error {
	return it.err
}

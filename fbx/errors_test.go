package fbx

import "testing"

func TestErrorsAreDefined(t *testing.T) {
	errs := []error{
		ErrNotFound,
		ErrAlreadyExists,
		ErrInvalidFormat,
		ErrCRCMismatch,
		ErrUnsupportedCodec,
		ErrPathInvalid,
		ErrLimitExceeded,
	}
	for i, err := range errs {
		if err == nil {
			t.Fatalf("error at index %d is nil", i)
		}
	}
}

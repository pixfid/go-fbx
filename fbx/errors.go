package fbx

import "errors"

var (
	ErrNotFound                    = errors.New("fbx: entry not found")
	ErrAlreadyExists               = errors.New("fbx: entry already exists")
	ErrInvalidFormat               = errors.New("fbx: invalid format")
	ErrCRCMismatch                 = errors.New("fbx: crc mismatch")
	ErrUnsupportedFeature          = errors.New("fbx: unsupported feature")
	ErrUnsupportedCodec            = errors.New("fbx: unsupported codec")
	ErrPathInvalid                 = errors.New("fbx: invalid path")
	ErrLimitExceeded               = errors.New("fbx: limit exceeded")
	ErrMigrationPreflightFailed    = errors.New("fbx: migration preflight failed")
	ErrMigrationInterrupted        = errors.New("fbx: migration interrupted")
	ErrMigrationVerificationFailed = errors.New("fbx: migration verification failed")
)

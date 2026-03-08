package fbx

import (
	"fmt"

	"github.com/pixfid/go-fbx/internal/format"
)

// Migrate upgrades a legacy v1 container to the v1 extension layout (IDX1 +
// required feature contract) without rewriting payload chunks.
func Migrate(path string, opts *MigrateOptions) error {
	cfg := MigrateOptions{VerifySource: VerifyDirectoryOnly}
	if opts != nil {
		cfg = *opts
		if cfg.VerifySource < VerifyDirectoryOnly || cfg.VerifySource > VerifyAllChunks {
			cfg.VerifySource = VerifyDirectoryOnly
		}
	}

	c, err := Open(path, nil)
	if err != nil {
		return err
	}
	defer c.Close()

	if _, err := c.Verify(&VerifyOptions{Mode: cfg.VerifySource}); err != nil {
		return fmt.Errorf("%w: %v", ErrMigrationPreflightFailed, err)
	}

	c.mu.RLock()
	alreadyExtended := headerHasDirIndexExtension(c.header)
	c.mu.RUnlock()
	if !alreadyExtended {
		tx, err := c.Begin()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrMigrationInterrupted, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("%w: %v", ErrMigrationInterrupted, err)
		}
	}

	if cfg.VerifyTarget {
		if _, err := c.Verify(&VerifyOptions{Mode: VerifyAllChunks}); err != nil {
			return fmt.Errorf("%w: %v", ErrMigrationVerificationFailed, err)
		}
	}
	return nil
}

func headerHasDirIndexExtension(h format.HeaderV1) bool {
	if h.Flags&format.HeaderFlagHasDirIndex == 0 || h.Flags&format.HeaderFlagHasRequiredFeatures == 0 {
		return false
	}
	if h.Reserved[headerReservedLayoutMarkerOffset] != headerLayoutMarkerV1Extension {
		return false
	}
	ptr := readHeaderDirIndexPointer(h)
	if ptr.offset == 0 || ptr.size == 0 || ptr.crc32 == 0 {
		return false
	}
	return true
}

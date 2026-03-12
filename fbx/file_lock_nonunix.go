//go:build !unix

package fbx

import "os"

func lockFileExclusive(_ *os.File) error { return nil }

func unlockFile(_ *os.File) error { return nil }

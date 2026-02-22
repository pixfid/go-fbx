package fbx

import (
	"runtime"
	"testing"
)

func TestDefaultOptions(t *testing.T) {
	o := defaultOptions()
	if o.ChunkSizeText != 1<<20 || o.ChunkSizeBin != 4<<20 {
		t.Fatalf("unexpected chunk sizes: text=%d bin=%d", o.ChunkSizeText, o.ChunkSizeBin)
	}
	if !o.DetectText || !o.StoreIfAlreadyCompressed || !o.SyncOnCommit || !o.StrictVerify {
		t.Fatalf("unexpected bool defaults: %+v", o)
	}
	if o.MaxWorkers != runtime.GOMAXPROCS(0) {
		t.Fatalf("unexpected workers default: %d", o.MaxWorkers)
	}
}

func TestVerifyModeConstants(t *testing.T) {
	if VerifyDirectoryOnly != 0 || VerifySampledChunks != 1 || VerifyAllChunks != 2 {
		t.Fatalf("unexpected verify constants")
	}
}

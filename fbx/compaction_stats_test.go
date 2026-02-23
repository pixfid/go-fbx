package fbx

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/pixfid/go-fbx/internal/format"
)

func TestCompactionStatsTrackChurnAndResetAfterPack(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "stats.fbx")
	packedPath := filepath.Join(dir, "packed.fbx")

	c, err := Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader(bytes.Repeat([]byte("a"), 512)), []byte(`{"v":1}`), &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	dead, churn := readStatsFromFile(t, containerPath)
	if dead != 0 || churn != 0 {
		t.Fatalf("expected zero counters after create+add, got dead=%d churn=%d", dead, churn)
	}

	c, err = Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open for replace: %v", err)
	}
	if err := c.Replace("a.fb2", bytes.NewReader(bytes.Repeat([]byte("b"), 1024)), []byte(`{"v":2}`), &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("replace: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close after replace: %v", err)
	}

	deadAfterReplace, churnAfterReplace := readStatsFromFile(t, containerPath)
	if deadAfterReplace == 0 {
		t.Fatalf("expected dead bytes > 0 after replace")
	}
	if churnAfterReplace != 1 {
		t.Fatalf("expected churn=1 after replace, got %d", churnAfterReplace)
	}

	c, err = Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open for remove: %v", err)
	}
	if err := c.Remove("a.fb2"); err != nil {
		_ = c.Close()
		t.Fatalf("remove: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close after remove: %v", err)
	}

	deadAfterRemove, churnAfterRemove := readStatsFromFile(t, containerPath)
	if deadAfterRemove <= deadAfterReplace {
		t.Fatalf("expected dead bytes to grow after remove, before=%d after=%d", deadAfterReplace, deadAfterRemove)
	}
	if churnAfterRemove != 2 {
		t.Fatalf("expected churn=2 after replace+remove, got %d", churnAfterRemove)
	}

	if err := Pack(containerPath, packedPath, &PackOptions{Codec: CodecStore, VerifyIn: false}); err != nil {
		t.Fatalf("pack out-of-place: %v", err)
	}
	deadPacked, churnPacked := readStatsFromFile(t, packedPath)
	if deadPacked != 0 || churnPacked != 0 {
		t.Fatalf("expected packed file counters reset to zero, got dead=%d churn=%d", deadPacked, churnPacked)
	}
}

func TestCompactionStatsUpsertNewDoesNotIncreaseChurn(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "upsert-new.fbx")
	c, err := Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Upsert("a.fb2", bytes.NewReader([]byte("abc")), nil, &WriteOptions{Codec: CodecStore}); err != nil {
		_ = c.Close()
		t.Fatalf("upsert new: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	dead, churn := readStatsFromFile(t, containerPath)
	if dead != 0 || churn != 0 {
		t.Fatalf("expected no churn for upsert of a new entry, got dead=%d churn=%d", dead, churn)
	}
}

func readStatsFromFile(t *testing.T, containerPath string) (uint64, uint64) {
	t.Helper()
	f, err := os.Open(containerPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()

	headBuf := make([]byte, format.HeaderSize)
	if _, err := f.ReadAt(headBuf, 0); err != nil {
		t.Fatalf("read header: %v", err)
	}
	h, err := format.UnmarshalHeaderV1(headBuf)
	if err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	return readCompactionStatsFromHeader(h)
}

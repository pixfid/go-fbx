package fbx

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"
)

func TestSetMetaPreservesPayloadAndDoesNotIncreaseCompactionStats(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "set-meta.fbx")
	body := bytes.Repeat([]byte("payload-"), 128)

	c, err := Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader(body), []byte(`{"v":1}`), nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	deadBefore, churnBefore := readStatsFromFile(t, containerPath)

	c, err = Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := c.SetMeta("a.fb2", []byte(`{"v":2}`)); err != nil {
		_ = c.Close()
		t.Fatalf("set meta: %v", err)
	}
	var out bytes.Buffer
	if err := c.Extract("a.fb2", &out); err != nil {
		_ = c.Close()
		t.Fatalf("extract: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close after set meta: %v", err)
	}
	if !bytes.Equal(out.Bytes(), body) {
		t.Fatalf("payload changed after metadata-only update")
	}

	deadAfter, churnAfter := readStatsFromFile(t, containerPath)
	if deadAfter != deadBefore || churnAfter != churnBefore {
		t.Fatalf("compaction stats changed on metadata-only update: before dead=%d churn=%d after dead=%d churn=%d", deadBefore, churnBefore, deadAfter, churnAfter)
	}
}

func TestSetMetaManyIgnoreMissing(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "set-meta-many.fbx")

	c, err := Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader([]byte("a")), []byte(`{"v":1}`), nil); err != nil {
		_ = c.Close()
		t.Fatalf("add a: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c, err = Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	updated, missing, err := c.SetMetaMany(map[string][]byte{
		"a.fb2":     []byte(`{"v":2}`),
		"ghost.fb2": []byte(`{"v":0}`),
	}, true)
	if err != nil {
		_ = c.Close()
		t.Fatalf("set-meta-many ignore missing: %v", err)
	}
	if updated != 1 || missing != 1 {
		_ = c.Close()
		t.Fatalf("unexpected counters: updated=%d missing=%d", updated, missing)
	}
	info, err := c.Stat("a.fb2")
	if err != nil {
		_ = c.Close()
		t.Fatalf("stat: %v", err)
	}
	_ = c.Close()
	if string(info.Meta) != `{"v":2}` {
		t.Fatalf("unexpected metadata: %s", string(info.Meta))
	}
}

func TestSetMetaManyMissingPathFailsWhenNotIgnored(t *testing.T) {
	dir := t.TempDir()
	containerPath := filepath.Join(dir, "set-meta-many-missing.fbx")

	c, err := Create(containerPath, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := c.Add("a.fb2", bytes.NewReader([]byte("a")), []byte(`{"v":1}`), nil); err != nil {
		_ = c.Close()
		t.Fatalf("add: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	c, err = Open(containerPath, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _, err = c.SetMetaMany(map[string][]byte{
		"a.fb2":     []byte(`{"v":2}`),
		"ghost.fb2": []byte(`{"v":0}`),
	}, false)
	if !errors.Is(err, ErrNotFound) {
		_ = c.Close()
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	info, statErr := c.Stat("a.fb2")
	_ = c.Close()
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}
	if string(info.Meta) != `{"v":1}` {
		t.Fatalf("metadata must remain unchanged on failed batch, got %s", string(info.Meta))
	}
}

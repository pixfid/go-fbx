package fbx

import (
	"path/filepath"
	"sync"
)

var processWriteLocks = struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}{
	locks: make(map[string]*sync.Mutex),
}

func acquireProcessWriteLock(path string) func() {
	key := filepath.Clean(path)
	processWriteLocks.mu.Lock()
	mu, ok := processWriteLocks.locks[key]
	if !ok {
		mu = &sync.Mutex{}
		processWriteLocks.locks[key] = mu
	}
	processWriteLocks.mu.Unlock()
	mu.Lock()
	return mu.Unlock
}

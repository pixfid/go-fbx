package fbx

import (
	"encoding/binary"
	"math"

	"github.com/pixfid/go-fbx/internal/format"
)

const (
	compactionDeadBytesOffset = 8
	compactionChurnOpsOffset  = 16
)

func readCompactionStatsFromHeader(h format.HeaderV1) (deadBytes uint64, churnOps uint64) {
	deadBytes = binary.LittleEndian.Uint64(h.Reserved[compactionDeadBytesOffset : compactionDeadBytesOffset+8])
	churnOps = binary.LittleEndian.Uint64(h.Reserved[compactionChurnOpsOffset : compactionChurnOpsOffset+8])
	return
}

func writeCompactionStatsToHeader(h *format.HeaderV1, deadBytes, churnOps uint64) {
	binary.LittleEndian.PutUint64(h.Reserved[compactionDeadBytesOffset:compactionDeadBytesOffset+8], deadBytes)
	binary.LittleEndian.PutUint64(h.Reserved[compactionChurnOpsOffset:compactionChurnOpsOffset+8], churnOps)
}

func saturatingAddUint64(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}

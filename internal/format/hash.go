package format

const (
	fnv64Offset = 14695981039346656037
	fnv64Prime  = 1099511628211
)

func FNV1a64(s string) uint64 {
	h := uint64(fnv64Offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= fnv64Prime
	}
	return h
}

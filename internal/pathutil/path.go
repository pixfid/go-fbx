package pathutil

import (
	"errors"
	"path"
	"strings"
	"unicode/utf8"
)

var ErrInvalidPath = errors.New("fbx: invalid path")

// Normalize validates and canonicalizes an FBX entry path.
func Normalize(p string) (string, error) {
	if p == "" {
		return "", ErrInvalidPath
	}
	if !utf8.ValidString(p) {
		return "", ErrInvalidPath
	}
	if strings.ContainsRune(p, '\x00') {
		return "", ErrInvalidPath
	}
	p = strings.ReplaceAll(p, "\\", "/")
	p = path.Clean(p)
	if p == "." || p == "" || strings.HasPrefix(p, "/") {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return p, nil
}

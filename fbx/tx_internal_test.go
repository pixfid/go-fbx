package fbx

import "testing"

func TestNormalizePrefix(t *testing.T) {
	if _, err := normalizePrefix(""); err == nil {
		t.Fatalf("expected error for empty prefix")
	}
	p, err := normalizePrefix("\\books\\a/")
	if err != nil {
		t.Fatalf("normalizePrefix: %v", err)
	}
	if p != "books/a" {
		t.Fatalf("unexpected normalized prefix: %q", p)
	}
}

func TestTextAndCompressedDetectors(t *testing.T) {
	if !textExt("a.fb2") || textExt("a.jpg") {
		t.Fatalf("textExt behavior mismatch")
	}
	if !compressedExt("a.jpg") || compressedExt("a.txt") {
		t.Fatalf("compressedExt behavior mismatch")
	}
	if !detectText("book.fb2", []byte("<FictionBook>ok</FictionBook>")) {
		t.Fatalf("expected text detection")
	}
	if detectText("img.bin", []byte{0x00, 0xff, 0x01}) {
		t.Fatalf("binary probe must not be text")
	}
	if !looksCompressed("a.jpg", []byte("xx")) {
		t.Fatalf("expected compressed by extension")
	}
	if !looksCompressed("a.bin", []byte{0x89, 0x50, 0x4e, 0x47}) {
		t.Fatalf("expected compressed by PNG magic")
	}
}

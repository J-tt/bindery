package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveDir(t *testing.T) {
	src := t.TempDir()
	// Simulate an audiobook release folder with multi-part m4b + cover.
	mustWrite(t, filepath.Join(src, "Part.01.m4b"), "part1")
	mustWrite(t, filepath.Join(src, "Part.02.m4b"), "part2")
	mustWrite(t, filepath.Join(src, "nested", "cover.jpg"), "cover")

	dst := filepath.Join(t.TempDir(), "Author", "Title (2020)")
	if err := MoveDir(src, dst); err != nil {
		t.Fatal(err)
	}

	// Source should be gone, destination should hold everything preserved.
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source should have been removed, err = %v", err)
	}
	for _, name := range []string{"Part.01.m4b", "Part.02.m4b", "nested/cover.jpg"} {
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Errorf("missing %s in destination: %v", name, err)
		}
	}
}

func TestMoveDirRefusesExistingDst(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "x.m4b"), "x")
	dst := t.TempDir() // already exists
	if err := MoveDir(src, dst); err == nil {
		t.Error("expected error when dst already exists")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

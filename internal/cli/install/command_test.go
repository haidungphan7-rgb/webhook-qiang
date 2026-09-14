package installcmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCopyExecutable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src.exe")

	if err := os.WriteFile(src, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(dir, "nested", "dst.exe")

	if err := copyExecutable(src, dst); err != nil {
		t.Fatalf("copyExecutable failed: %v", err)
	}

	data, err := os.ReadFile(dst) // #nosec G304 -- test path.
	if err != nil {
		t.Fatal(err)
	}

	if string(data) != "binary" {
		t.Errorf("copied content = %q, want %q", data, "binary")
	}

	if fi, err := os.Stat(dst); err != nil || fi.IsDir() {
		t.Errorf("destination is not a regular file: %v", err)
	}
}

func TestSamePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "webhook-zq.exe")

	if err := os.WriteFile(a, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if !samePath(a, a) {
		t.Error("samePath(a, a) = false, want true")
	}

	if samePath(a, filepath.Join(dir, "other.exe")) {
		t.Error("samePath compared different files as equal")
	}

	if runtime.GOOS == "windows" {
		lower := filepath.Join(dir, "WEBHOOK-ZQ.EXE")
		if !samePath(a, lower) {
			t.Errorf("samePath(%q, %q) = false on case-insensitive Windows", a, lower)
		}
	}
}

package kernel

import (
	"os"
	"path/filepath"
	"testing"
)

// SDD §14.1: the local key file is created with mode 0600, and a looser mode is refused.
func TestLocalSealer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "key")
	s, err := LocalSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode %v (%v), want 0600", info.Mode().Perm(), err)
	}
	sealed, err := s.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	again, err := LocalSealer(path)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := again.Open(sealed); err != nil || string(plain) != "secret" {
		t.Errorf("reopened key: %q, %v", plain, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LocalSealer(path); err == nil {
		t.Error("a key file that others can read was accepted")
	}
}

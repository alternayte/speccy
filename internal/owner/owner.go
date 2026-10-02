// Package owner is the owner lock of a local state folder. One local process owns a state
// folder: its store, its review worker and its watch on the files. The first process to start
// takes the lock; each later one is a client process, which sends its API calls to the owner
// and opens no store.
package owner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// File is the name of the lock file in the state folder.
const File = "owner.lock"

// ErrHeld means that another process holds the lock: it is the owner.
var ErrHeld = errors.New("another process owns this state folder")

// Info is what the lock file holds, so a client process reads it with no call.
type Info struct {
	// Address is the owner's listener for processes, as host:port on 127.0.0.1. It is empty
	// while the owner starts.
	Address string `json:"address"`
	// Token is the secret a call to that listener carries. A web page in the browser can reach
	// a loopback port, and it cannot read a file in the state folder.
	Token   string `json:"token"`
	Version string `json:"version"`
	// Kind names the process for a person, such as "the app" or "speccy mcp".
	Kind string `json:"kind"`
	PID  int    `json:"pid"`
}

// Lock is the owner lock, held by this process. The operating system frees it when the
// process ends, so a hard kill leaves no lock that a person has to remove.
type Lock struct {
	f *os.File
}

// Take takes the owner lock of the state folder, and returns ErrHeld when another process
// holds it. It never waits.
func Take(stateDir string) (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(stateDir, File), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := lockFile(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Write stores what a client process needs in the lock file.
func (l *Lock) Write(info Info) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	if _, err := l.f.WriteAt(raw, 0); err != nil {
		return err
	}
	return l.f.Sync()
}

// Release empties the lock file and frees the lock.
func (l *Lock) Release() {
	_ = l.f.Truncate(0)
	_ = l.f.Close()
}

// Read returns what the owner wrote in the lock file. ok is false while the file is empty: no
// process owns the folder, or the owner has not written yet.
func Read(stateDir string) (info Info, ok bool, err error) {
	f, err := os.Open(filepath.Join(stateDir, File))
	if errors.Is(err, os.ErrNotExist) {
		return info, false, nil
	}
	if err != nil {
		return info, false, err
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 1<<16))
	if err != nil {
		return info, false, err
	}
	// The owner truncates and writes in two steps, so a reader can see a part of the text.
	if json.Unmarshal(raw, &info) != nil {
		return Info{}, false, nil
	}
	return info, true, nil
}

// NewToken returns a random token for the owner's listener.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

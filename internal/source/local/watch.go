package local

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/alternayte/speccy/internal/source"
)

// Watch calls onChange after files under the root change, at most once per quiet period.
// It returns when ctx ends. REQ-005: external changes create versions.
func (r *Root) Watch(ctx context.Context, quiet time.Duration, onChange func()) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()
	r.addWatches(w)

	var timer *time.Timer
	fire := make(chan struct{}, 1)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if isOwnTemp(ev.Name) || r.isState(ev.Name) {
				continue
			}
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(quiet, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
		case <-fire:
			r.addWatches(w) // new folders need a watch
			onChange()
		case _, ok := <-w.Errors:
			if !ok {
				return nil
			}
		}
	}
}

// isState reports whether p is in .speccy/state, where Speccy writes its own database.
func (r *Root) isState(p string) bool {
	state := filepath.Join(r.dir, ".speccy", "state")
	return p == state || strings.HasPrefix(p, state+string(filepath.Separator))
}

// addWatches watches every folder that a scan reads, .speccy and .speccy/profiles for profile
// changes, and the sidecars, where a waiver written by hand lives (DEC-009). .speccy.yaml is in
// the root, which is watched.
func (r *Root) addWatches(w *fsnotify.Watcher) {
	extras := []string{filepath.Join(r.dir, ".speccy"), filepath.Join(r.dir, ".speccy", "profiles")}
	decisions := r.abs(source.SidecarDir)
	_ = filepath.WalkDir(decisions, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			extras = append(extras, p)
		}
		return nil
	})
	for _, extra := range extras {
		if info, err := os.Stat(extra); err == nil && info.IsDir() {
			_ = w.Add(extra)
		}
	}
	_ = filepath.WalkDir(r.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable folder is skipped; the scan reports it
		}
		if !d.IsDir() {
			return nil
		}
		if p != r.dir && skipDir(d.Name()) {
			return filepath.SkipDir
		}
		_ = w.Add(p) // adding a watched folder again is a no-op
		return nil
	})
}

func isOwnTemp(name string) bool {
	m, _ := filepath.Match(".speccy-*", filepath.Base(name))
	return m
}

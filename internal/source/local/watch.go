package local

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
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
			if isOwnTemp(ev.Name) {
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

func (r *Root) addWatches(w *fsnotify.Watcher) {
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

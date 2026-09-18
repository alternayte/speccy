package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SDD §13.4: the bundle route's initial JavaScript is at most 300 kB gzipped,
// excluding the lazy Mermaid chunk.
const maxInitialJSGzip = 300 * 1000

const (
	webDist      = "web/dist"
	viteManifest = "web/dist/.vite/manifest.json"
	// bundleRoute is the source of the bundle route. The router splits its component into a
	// lazy chunk, which the bundle screen always loads, so it counts as initial JavaScript.
	bundleRoute = "src/routes/bundles/$bundleId/index.tsx"
)

type manifestChunk struct {
	File    string   `json:"file"`
	IsEntry bool     `json:"isEntry"`
	Imports []string `json:"imports"`
}

// initialJS returns the JavaScript files that the bundle route needs before it renders: the
// entry chunk, the bundle route's chunks, and their static imports. Dynamic imports, such as
// the Mermaid chunk, are not counted.
func initialJS(manifest map[string]manifestChunk) []string {
	seen := map[string]bool{}
	var files []string
	var visit func(key string)
	visit = func(key string) {
		if seen[key] {
			return
		}
		seen[key] = true
		c, ok := manifest[key]
		if !ok {
			return
		}
		if strings.HasSuffix(c.File, ".js") {
			files = append(files, c.File)
		}
		for _, imp := range c.Imports {
			visit(imp)
		}
	}
	keys := make([]string, 0, len(manifest))
	for k := range manifest {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if manifest[k].IsEntry || k == bundleRoute || strings.HasPrefix(k, bundleRoute+"?") {
			visit(k)
		}
	}
	sort.Strings(files)
	return files
}

func gzipSize(b []byte) (int, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return 0, err
	}
	if _, err := w.Write(b); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	return buf.Len(), nil
}

func cmdBudget() error {
	raw, err := os.ReadFile(viteManifest)
	if err != nil {
		return fmt.Errorf("budget: %w. Build the web app first", err)
	}
	var manifest map[string]manifestChunk
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("budget: %s: %w", viteManifest, err)
	}
	if !hasRoute(manifest) {
		return fmt.Errorf("budget: %s has no chunk for %s; update bundleRoute in tools/buildtool/budget.go", viteManifest, bundleRoute)
	}
	files := initialJS(manifest)
	total := 0
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(webDist, f))
		if err != nil {
			return fmt.Errorf("budget: %w", err)
		}
		n, err := gzipSize(b)
		if err != nil {
			return fmt.Errorf("budget: %w", err)
		}
		fmt.Printf("initial: %s %.1f kB gzip\n", f, float64(n)/1000)
		total += n
	}
	if total > maxInitialJSGzip {
		return fmt.Errorf("budget: initial JavaScript is %.1f kB gzip; the limit is %d kB", float64(total)/1000, maxInitialJSGzip/1000)
	}
	fmt.Printf("budget: initial JavaScript %.1f kB gzip, limit %d kB\n", float64(total)/1000, maxInitialJSGzip/1000)
	return nil
}

func hasRoute(manifest map[string]manifestChunk) bool {
	for k := range manifest {
		if k == bundleRoute || strings.HasPrefix(k, bundleRoute+"?") {
			return true
		}
	}
	return false
}

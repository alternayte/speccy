package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SDD §17: no common/ or utils/ packages.
var bannedDirs = map[string]bool{"common": true, "utils": true, "util": true, "helpers": true}

// SDD §17: route files contain no business logic. A route file imports only the router,
// React, types, and the feature or UI component it renders.
var routeImportAllowed = []string{
	"@tanstack/react-router",
	"react",
	"@/features/",
	"@/components/",
}

var importRe = regexp.MustCompile(`(?m)^\s*import\s+(type\s+)?[^'"]*?['"]([^'"]+)['"]`)

func cmdConventions() error {
	var problems []string
	for _, root := range []string{"cmd", "internal", "tools", "web/src"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			if d.IsDir() && bannedDirs[d.Name()] {
				problems = append(problems, fmt.Sprintf("%s: a %s/ package is banned (SDD §17). Put the code in the feature that uses it.", p, d.Name()))
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	routes := "web/src/routes"
	err := filepath.WalkDir(routes, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".ts") && !strings.HasSuffix(p, ".tsx") {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range importRe.FindAllStringSubmatch(string(src), -1) {
			typeOnly, from := m[1] != "", m[2]
			if typeOnly || routeImportOK(from) {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s: imports %q. A route file renders a feature and holds no logic (SDD §17). Move the code to web/src/features/.", p, from))
		}
		return nil
	})
	if err != nil {
		return err
	}

	for _, p := range problems {
		fmt.Println(p)
	}
	if len(problems) > 0 {
		return fmt.Errorf("conventions: %d problems", len(problems))
	}
	fmt.Println("conventions: clean")
	return nil
}

func routeImportOK(from string) bool {
	for _, a := range routeImportAllowed {
		if from == a || strings.HasSuffix(a, "/") && strings.HasPrefix(from, a) {
			return true
		}
	}
	return false
}

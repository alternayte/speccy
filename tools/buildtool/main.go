// Command buildtool runs the repository checks that are not a linter rule:
// the JavaScript budget (SDD §13.4), the code organisation conventions (SDD §17), the
// SQLite query adapter (SDD §11.2), the docs site's generated pages and their checks, and
// the gauntlet screenshots (BUILD.md §6).
package main

import (
	"fmt"
	"os"
)

func main() {
	// gauntlet needs its run, docs-shots takes an optional part, and the rest take nothing.
	args := os.Args[1:]
	ok := len(args) == 1 && args[0] != "gauntlet" || len(args) == 2 && (args[0] == "gauntlet" || args[0] == "docs-shots")
	if !ok {
		fmt.Fprintln(os.Stderr, "usage: buildtool budget|conventions|sqladapter|docs-ref|docs-ref-check|docs-shots [linked]|docs-shots-github|gauntlet <run>")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "budget":
		err = cmdBudget()
	case "conventions":
		err = cmdConventions()
	case "sqladapter":
		err = cmdSQLAdapter()
	case "docs-ref":
		err = cmdDocsRef(false)
	case "docs-ref-check":
		err = cmdDocsRef(true)
	case "docs-shots-github":
		err = cmdDocsShotsGitHub()
	case "docs-shots":
		part := ""
		if len(os.Args) == 3 {
			part = os.Args[2]
		}
		err = cmdDocsShots(part)
	case "gauntlet":
		err = cmdGauntlet(os.Args[2])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildtool:", err)
		os.Exit(1)
	}
}

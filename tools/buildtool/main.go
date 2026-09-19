// Command buildtool runs the repository checks that are not a linter rule:
// the JavaScript budget (SDD §13.4), the code organisation conventions (SDD §17), the
// SQLite query adapter (SDD §11.2), and the gauntlet screenshots (BUILD.md §6).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] == "gauntlet") != (len(os.Args) == 3) || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: buildtool budget|conventions|sqladapter|gauntlet <run>")
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

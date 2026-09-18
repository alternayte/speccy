// Command buildtool runs the repository checks that are not a linter rule:
// the JavaScript budget (SDD §13.4), the code organisation conventions (SDD §17), and the
// SQLite query adapter (SDD §11.2).
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: buildtool budget|conventions|sqladapter")
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
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "buildtool:", err)
		os.Exit(1)
	}
}

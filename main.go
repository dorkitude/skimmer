// Command skimmer skims web pages, files, and lists with Jev, returning only
// the parts that help with a goal.
package main

import (
	"os"

	"github.com/dorkitude/skimmer/cmd"
)

func main() {
	os.Exit(cmd.Execute())
}

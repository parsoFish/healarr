// Command healarr is the single binary: CLI + agent daemon.
package main

import (
	"fmt"
	"os"

	"github.com/parsoFish/healarr/internal/cli"
)

func main() {
	if err := cli.NewRootCmd(cli.DefaultDeps()).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "healarr:", err)
		os.Exit(1)
	}
}

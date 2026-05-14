package main

import (
	"os"

	"codeberg.org/mwtrigg/zbxctl/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

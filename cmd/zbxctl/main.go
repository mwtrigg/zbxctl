package main

import (
	"os"

	"github.com/mwtrigg/zbxctl/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}

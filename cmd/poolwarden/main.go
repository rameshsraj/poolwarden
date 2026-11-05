package main

import (
	"os"

	"github.com/smarsh/poolwarden/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:], cli.DefaultDeps()))
}

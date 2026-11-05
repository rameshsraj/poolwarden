// gendocs generates man pages and markdown docs from the cobra tree.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra/doc"

	"github.com/smarsh/poolwarden/internal/cli"
)

func main() {
	root := cli.NewRoot(&cli.Deps{
		Stdout: os.Stdout, Stderr: os.Stderr,
		FS: nil, UID: 0,
	})
	root.DisableAutoGenTag = true
	for _, c := range root.Commands() {
		c.DisableAutoGenTag = true
	}
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: gendocs man|md <dir>")
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "man":
		err = doc.GenManTree(root, nil, os.Args[2])
	case "md":
		err = doc.GenMarkdownTree(root, os.Args[2])
	default:
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

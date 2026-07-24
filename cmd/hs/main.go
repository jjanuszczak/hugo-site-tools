package main

import (
	"os"

	"github.com/johnjanuszczak/hugo-site-tools/internal/app"
)

func main() {
	os.Exit(app.Run(os.Args[1:], os.Stdout, os.Stderr))
}

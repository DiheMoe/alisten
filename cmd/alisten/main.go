package main

import (
	"os"

	"github.com/dihemoe/alisten/internal/bootstrap"
)

func main() {
	os.Exit(bootstrap.Main(os.Args[1:]))
}

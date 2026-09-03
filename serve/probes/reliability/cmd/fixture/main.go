package main

import (
	"fmt"
	"os"

	"canary/internal/canary"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/fixture <dir>")
		os.Exit(2)
	}
	if err := canary.CreateFixture(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(os.Args[1])
}

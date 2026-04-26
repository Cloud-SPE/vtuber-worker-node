package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "directory to scan recursively")
	flag.Parse()

	findings, err := checkTree(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "payment-middleware-check: walk %s: %v\n", *root, err)
		os.Exit(2)
	}
	for _, f := range findings {
		fmt.Fprintln(os.Stderr, f.format())
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "\npayment-middleware-check: %d violation(s) — core belief #3 requires paid routes register via RegisterPaidRoute.\n", len(findings))
		os.Exit(1)
	}
}

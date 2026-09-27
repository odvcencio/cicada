// Command cicada-ebnf writes the readable EBNF appendix from the grammar DSL.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"m31labs.dev/cicada/language/grammar"
)

func main() {
	output := flag.String("o", "", "output file (default: stdout)")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: cicada-ebnf [-o output]")
		os.Exit(2)
	}
	content, err := grammar.EBNF()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cicada-ebnf:", err)
		os.Exit(1)
	}
	if *output == "" {
		_, err = os.Stdout.WriteString(content)
	} else {
		err = os.MkdirAll(filepath.Dir(*output), 0o755)
		if err == nil {
			err = os.WriteFile(*output, []byte(content), 0o644)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cicada-ebnf:", err)
		os.Exit(1)
	}
}

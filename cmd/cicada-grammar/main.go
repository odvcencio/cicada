// Command cicada-grammar generates the Cicada parser blob from the grammargen
// Go DSL in package grammar and reports on the grammar: table sizes, resolved
// conflicts, embedded tests, and an optional sample parse.
package main

import (
	"flag"
	"fmt"
	"os"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammargen"

	"m31labs.dev/cicada/language/grammar"
)

func main() {
	bin := flag.String("bin", "", "write the parser blob to this path")
	sample := flag.String("sample", "", "parse this score and print its tree")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: cicada-grammar [-bin cicada.bin] [-sample score.cicada]")
		os.Exit(2)
	}
	if err := run(*bin, *sample); err != nil {
		fmt.Fprintln(os.Stderr, "cicada-grammar:", err)
		os.Exit(1)
	}
}

func run(bin, sample string) error {
	g := grammar.Cicada()
	if warnings := grammargen.Validate(g); len(warnings) > 0 {
		return fmt.Errorf("grammar warnings: %v", warnings)
	}
	report, err := grammargen.GenerateWithReport(g)
	if err != nil {
		return err
	}
	blob, err := grammargen.Generate(g)
	if err != nil {
		return err
	}
	if err := grammargen.RunTests(g); err != nil {
		return err
	}
	fmt.Printf("Grammar:   %s\n", g.Name)
	fmt.Printf("Rules:     %d\n", len(g.RuleOrder))
	fmt.Printf("States:    %d\n", report.StateCount)
	fmt.Printf("Tokens:    %d\n", report.TokenCount)
	fmt.Printf("Blob:      %d bytes\n", len(blob))
	fmt.Printf("Conflicts: %d resolved\n", len(report.Conflicts))
	fmt.Printf("Tests:     %d passed\n", len(g.Tests))
	if sample != "" {
		src, err := os.ReadFile(sample)
		if err != nil {
			return err
		}
		tree, err := gts.NewParser(report.Language).Parse(src)
		if err != nil {
			return err
		}
		root := tree.RootNode()
		fmt.Printf("Sample:    %s (%d bytes)\n", sample, len(src))
		fmt.Printf("S-expression:\n%s\n", root.SExpr(report.Language))
		if root.HasErrorOrMissing() {
			return fmt.Errorf("%s does not parse", sample)
		}
	}
	if bin != "" {
		if err := os.WriteFile(bin, blob, 0o644); err != nil {
			return err
		}
		fmt.Printf("Wrote:     %s\n", bin)
	}
	return nil
}

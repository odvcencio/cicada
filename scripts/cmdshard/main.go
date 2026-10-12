// Command cmdshard splits the cmd/cicada tests across parallel CI jobs.
//
// Usage:
//
//	cmdshard run K/N [-- go test flags]   run shard K of N (K counts from 1)
//	cmdshard list K/N                     print the tests in shard K of N
//	cmdshard check N                      verify the N shards cover every test once
//
// Tests are assigned heaviest first to the lightest shard, using the recorded
// seconds in weights.txt. A test missing from weights.txt gets a default
// weight, so a new test lands in a shard with no further edit.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	pkg           = "./cmd/cicada"
	defaultWeight = 0.5 // seconds, for tests absent from weights.txt
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cmdshard:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: cmdshard run K/N [-- go test flags] | list K/N | check N")
	}
	switch args[0] {
	case "check":
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 {
			return fmt.Errorf("bad shard count %q", args[1])
		}
		return check(n)
	case "list", "run":
		k, n, err := parseShard(args[1])
		if err != nil {
			return err
		}
		tests, err := goList()
		if err != nil {
			return err
		}
		shards := assign(tests, loadWeights(), n)
		if args[0] == "list" {
			fmt.Println(strings.Join(shards[k-1], "\n"))
			return nil
		}
		if len(shards[k-1]) == 0 {
			return fmt.Errorf("shard %d/%d is empty", k, n)
		}
		extra := args[2:]
		if len(extra) > 0 && extra[0] == "--" {
			extra = extra[1:]
		}
		names := make([]string, len(shards[k-1]))
		for i, name := range shards[k-1] {
			names[i] = regexp.QuoteMeta(name)
		}
		cmdArgs := append([]string{"test", pkg, "-count=1", "-run", "^(" + strings.Join(names, "|") + ")$"}, extra...)
		fmt.Printf("shard %d/%d: %d of %d tests\n", k, n, len(shards[k-1]), len(tests))
		cmd := exec.Command("go", cmdArgs...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func parseShard(s string) (k, n int, err error) {
	a, b, ok := strings.Cut(s, "/")
	if ok {
		k, err = strconv.Atoi(a)
		if err == nil {
			n, err = strconv.Atoi(b)
		}
	}
	if !ok || err != nil || n < 1 || k < 1 || k > n {
		return 0, 0, fmt.Errorf("shard must look like K/N with 1 <= K <= N, got %q", s)
	}
	return k, n, nil
}

// goList returns the sorted names that `go test -list .` reports for the package.
func goList() ([]string, error) {
	cmd := exec.Command("go", "test", pkg, "-list", ".")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go test -list: %w", err)
	}
	var tests []string
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		// Skip the trailing "ok  <pkg>  <time>" line and benchmarks, which -run does not select.
		if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "Example") || strings.HasPrefix(name, "Fuzz") {
			tests = append(tests, name)
		}
	}
	sort.Strings(tests)
	if len(tests) == 0 {
		return nil, errors.New("go test -list found no tests")
	}
	return tests, nil
}

func loadWeights() map[string]float64 {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "weights.txt"))
	if err != nil {
		// Fall back to the working directory when the source path is gone.
		data, err = os.ReadFile("scripts/cmdshard/weights.txt")
	}
	weights := make(map[string]float64)
	if err != nil {
		return weights
	}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || strings.HasPrefix(f[0], "#") {
			continue
		}
		if w, err := strconv.ParseFloat(f[1], 64); err == nil {
			weights[f[0]] = w
		}
	}
	return weights
}

// assign places tests heaviest first on the lightest shard. Ties break by name
// and by lowest shard index, so the result depends only on the inputs.
func assign(tests []string, weights map[string]float64, n int) [][]string {
	order := append([]string(nil), tests...)
	weight := func(name string) float64 {
		if w, ok := weights[name]; ok {
			return w
		}
		return defaultWeight
	}
	sort.SliceStable(order, func(i, j int) bool {
		wi, wj := weight(order[i]), weight(order[j])
		if wi != wj {
			return wi > wj
		}
		return order[i] < order[j]
	})
	shards := make([][]string, n)
	load := make([]float64, n)
	for _, name := range order {
		best := 0
		for s := 1; s < n; s++ {
			if load[s] < load[best] {
				best = s
			}
		}
		shards[best] = append(shards[best], name)
		load[best] += weight(name)
	}
	for _, s := range shards {
		sort.Strings(s)
	}
	return shards
}

// check fails unless the shards' union equals the go test -list output with no repeats.
func check(n int) error {
	tests, err := goList()
	if err != nil {
		return err
	}
	weights := loadWeights()
	shards := assign(tests, weights, n)
	seen := make(map[string]int)
	var problems []string
	for k, shard := range shards {
		for _, name := range shard {
			if prev, dup := seen[name]; dup {
				problems = append(problems, fmt.Sprintf("%s is in shard %d and shard %d", name, prev, k+1))
			}
			seen[name] = k + 1
		}
		if len(shard) == 0 {
			problems = append(problems, fmt.Sprintf("shard %d/%d is empty", k+1, n))
		}
	}
	for _, name := range tests {
		if seen[name] == 0 {
			problems = append(problems, name+" is in no shard")
		}
	}
	if len(seen) != len(tests) {
		problems = append(problems, fmt.Sprintf("shards hold %d distinct tests, go test -list reports %d", len(seen), len(tests)))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "\n"))
	}
	listed := make(map[string]bool, len(tests))
	for _, t := range tests {
		listed[t] = true
	}
	stale := 0
	for name := range weights {
		if !listed[name] {
			stale++
		}
	}
	fmt.Printf("ok: %d tests in %d shards, none missing, none repeated", len(tests), n)
	if stale > 0 {
		fmt.Printf(" (%d weights.txt entries match no test)", stale)
	}
	fmt.Println()
	for k, shard := range shards {
		var total float64
		for _, name := range shard {
			if w, ok := weights[name]; ok {
				total += w
			} else {
				total += defaultWeight
			}
		}
		fmt.Printf("  shard %d/%d: %d tests, %.0f s estimated\n", k+1, n, len(shard), total)
	}
	return nil
}

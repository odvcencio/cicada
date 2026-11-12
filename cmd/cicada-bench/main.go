// Command cicada-bench times the steps of an agent's loop (check, apply,
// render, loudness render, audition) with a built cicada binary on named
// reference scores, prints a table, writes JSON, and with --enforce fails when
// a speed gate does not hold.
//
// Each step runs the binary as a separate process, as an agent does, once to
// warm up and then --runs times, and keeps the median. The output names the
// CPUs the runs could use; pin them with taskset on Linux. Run it from the
// repository root:
//
//	go build -o build/cicada ./cmd/cicada
//	go run ./cmd/cicada-bench --cicada build/cicada --json build/bench.json
//
// The gates and the reasons for their numbers are in gates.go.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Reference scores. Small is one track for 7 seconds, medium is four tracks for
// 95 seconds, large is twelve tracks for 181 seconds.
var defaultScores = map[string]string{
	"small":  "examples/tuplet-grid.cicada",
	"medium": "examples/songs/lanterns-after-rain.cicada",
	"large":  "examples/songs/anywhere-you-are.cicada",
}

// defaultSteps is the set CI runs: the plan's first gates.
const defaultSteps = "help,check:large,apply1:large,apply50:large,render:medium,render:large,render-jobs1:large,render-jobs4:large,render-auto:medium,render-auto:large,loudness:medium,audition:large"

type options struct {
	cicada    string
	runs      int
	steps     []string
	scores    map[string]string
	largeBars int
	loudness  float64
	jsonPath  string
	enforce   bool
}

// Result is one step's measurement. Times are in milliseconds.
type Result struct {
	Step      string    `json:"step"`
	Score     string    `json:"score,omitempty"`
	Args      []string  `json:"args"`
	Bars      int       `json:"bars,omitempty"`
	RunsMS    []float64 `json:"runs_ms"`
	MedianMS  float64   `json:"median_ms"`
	MinMS     float64   `json:"min_ms"`
	MaxMS     float64   `json:"max_ms"`
	PeakRSSMB float64   `json:"peak_rss_mb,omitempty"`
	// AudioSeconds and Realtime are set for renders: the rendered length and
	// that length divided by the median wall time.
	AudioSeconds float64 `json:"audio_seconds,omitempty"`
	Realtime     float64 `json:"realtime,omitempty"`
	// Passes and Syntheses are set for loudness renders. Syntheses is -1 when
	// the binary does not report it.
	Passes    int `json:"passes,omitempty"`
	Syntheses int `json:"syntheses,omitempty"`
	Replays   int `json:"replays,omitempty"`
	Repeats   int `json:"repeats,omitempty"`
	ExitCode  int `json:"exit_code"`
	// Jobs is the --jobs value of a render-jobsN step. SHA256 is the
	// rendered WAV's hash, set for render steps when every run wrote the
	// same bytes; UnstableHash is set when two runs did not.
	Jobs         int    `json:"jobs,omitempty"`
	Auto         bool   `json:"auto,omitempty"` // render-auto: --jobs auto
	SHA256       string `json:"sha256,omitempty"`
	UnstableHash bool   `json:"unstable_hash,omitempty"`
}

// renderJobs returns N for a render-jobsN step kind, and 0 for render-auto
// (--jobs auto).
func renderJobs(kind string) (int, bool) {
	if kind == "render-auto" {
		return 0, true
	}
	digits, ok := strings.CutPrefix(kind, "render-jobs")
	if !ok {
		return 0, false
	}
	jobs, err := strconv.Atoi(digits)
	return jobs, err == nil && jobs >= 1 && jobs <= 64 && strconv.Itoa(jobs) == digits
}

// LoudnessRatio is a loudness render's time over a plain render's.
type LoudnessRatio struct {
	Step  string  `json:"step"`
	Ratio float64 `json:"ratio"`
}

// Report is the JSON document --json writes. It follows the conventions of
// the cicada --json documents (docs/manual/agents.md): a schema field,
// snake_case keys, and paths relative to the working folder when inside it.
type Report struct {
	Schema string `json:"schema"`
	Cicada string `json:"cicada"`
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	CPUs   int    `json:"cpus"`
	// CPUList is the processors the runs may use, as the host reports it
	// (Linux: the affinity list, such as 4-7 under taskset), or "all".
	CPUList    string   `json:"cpu_list"`
	LoadBefore string   `json:"load_before,omitempty"`
	LoadAfter  string   `json:"load_after,omitempty"`
	Runs       int      `json:"runs"`
	Results    []Result `json:"results"`
	// LoudnessRatios are each loudness render's median over the plain render
	// of the same score and bars: reported, not gated.
	LoudnessRatios []LoudnessRatio `json:"loudness_ratios,omitempty"`
	Gates          []GateOutcome   `json:"gates"`
	Enforced       bool            `json:"enforced"`
	Passed         bool            `json:"passed"`
}

func main() {
	o, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "cicada-bench:", err)
		os.Exit(2)
	}
	report, err := run(o, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cicada-bench:", err)
		os.Exit(1)
	}
	if o.jsonPath != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(o.jsonPath, append(data, '\n'), 0o644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "cicada-bench:", err)
			os.Exit(1)
		}
	}
	if o.enforce && !report.Passed {
		fmt.Fprintln(os.Stderr, "cicada-bench: a speed gate failed")
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("cicada-bench", flag.ContinueOnError)
	o := options{scores: map[string]string{}}
	flags.StringVar(&o.cicada, "cicada", "build/cicada", "cicada binary to measure")
	flags.IntVar(&o.runs, "runs", 5, "runs per step; the median is kept")
	steps := flags.String("steps", defaultSteps, "comma-separated steps: help, check:<size>, apply1:<size>, apply50:<size>, render:<size>, render-jobs<N>:<size> (render with --jobs N), render-auto:<size> (--jobs auto), loudness:<size>, audition:<size>; size is small, medium or large")
	for _, size := range []string{"small", "medium", "large"} {
		o.scores[size] = defaultScores[size]
		flags.Func(size, "score for the "+size+" steps (default "+defaultScores[size]+")", func(value string) error {
			o.scores[size] = value
			return nil
		})
	}
	flags.IntVar(&o.largeBars, "large-bars", 0, "render and audition only the first N bars of the large score; 0 is the whole song")
	flags.Float64Var(&o.loudness, "loudness", -14, "loudness target in LUFS for loudness steps")
	flags.StringVar(&o.jsonPath, "json", "", "write the results as JSON to this file")
	flags.BoolVar(&o.enforce, "enforce", false, "exit 1 when a gate fails")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() != 0 {
		return o, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if o.runs < 1 {
		return o, errors.New("--runs must be at least 1")
	}
	if o.largeBars < 0 {
		return o, errors.New("--large-bars must not be negative")
	}
	for _, step := range strings.Split(*steps, ",") {
		step = strings.TrimSpace(step)
		if step == "" {
			continue
		}
		if err := validStep(step); err != nil {
			return o, err
		}
		o.steps = append(o.steps, step)
	}
	if len(o.steps) == 0 {
		return o, errors.New("no steps")
	}
	return o, nil
}

func validStep(step string) error {
	if step == "help" {
		return nil
	}
	kind, size, ok := strings.Cut(step, ":")
	_, jobs := renderJobs(kind)
	if !ok || !jobs && !slices.Contains([]string{"check", "apply1", "apply50", "render", "loudness", "audition"}, kind) || defaultScores[size] == "" {
		return fmt.Errorf("unknown step %q", step)
	}
	return nil
}

func run(o options, out io.Writer) (Report, error) {
	binary, err := filepath.Abs(o.cicada)
	if err != nil {
		return Report{}, err
	}
	if _, err := os.Stat(binary); err != nil {
		return Report{}, fmt.Errorf("cicada binary: %w", err)
	}
	work, err := os.MkdirTemp("", "cicada-bench-*")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(work)
	report := Report{Schema: "cicada.bench.v1", Cicada: displayPath(binary), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(), CPUList: cpuList(), LoadBefore: loadAverage(), Runs: o.runs, Enforced: o.enforce}
	fmt.Fprintf(out, "cicada-bench: %s on %s/%s, %d CPUs (%s), load %s, %d runs per step after 1 warm-up run\n", binary, runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), report.CPUList, report.LoadBefore, o.runs)
	for _, step := range o.steps {
		result, err := measure(o, binary, work, step)
		if err != nil {
			return report, fmt.Errorf("%s: %w", step, err)
		}
		fmt.Fprintln(out, formatResult(result))
		report.Results = append(report.Results, result)
	}
	report.LoadAfter = loadAverage()
	for _, result := range report.Results {
		if strings.HasPrefix(result.Step, "loudness:") {
			if ratio := loudnessRatio(result, report.Results); !math.IsNaN(ratio) {
				report.LoudnessRatios = append(report.LoudnessRatios, LoudnessRatio{Step: result.Step, Ratio: ratio})
				fmt.Fprintf(out, "%s takes %.2f times the plain render (reported, not a gate)\n", result.Step, ratio)
			}
		}
	}
	report.Gates, report.Passed = checkGates(report.Results)
	fmt.Fprintln(out, "gates:")
	for _, gate := range report.Gates {
		fmt.Fprintln(out, "  "+gate.String())
	}
	fmt.Fprintf(out, "load after: %s\n", report.LoadAfter)
	return report, nil
}

// measure runs one step o.runs times in its own folder and keeps the median.
func measure(o options, binary, work, step string) (Result, error) {
	result := Result{Step: step}
	kind, size, _ := strings.Cut(step, ":")
	dir := filepath.Join(work, strings.ReplaceAll(step, ":", "-"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return result, err
	}
	var score string
	if size != "" {
		var err error
		score, err = copyScore(o.scores[size], dir)
		if err != nil {
			return result, err
		}
		result.Score = filepath.Base(o.scores[size])
		if _, jobs := renderJobs(kind); size == "large" && o.largeBars > 0 && (kind == "render" || jobs || kind == "audition" || kind == "loudness") {
			result.Bars = o.largeBars
		}
	}
	var args []string
	switch kind {
	case "help":
		args = []string{"help"}
	case "check":
		args = []string{"check", score}
	case "apply1", "apply50":
		count := 1
		if kind == "apply50" {
			count = 50
		}
		if err := writeIntents(filepath.Join(dir, score), filepath.Join(dir, "intents.json"), count); err != nil {
			return result, err
		}
		args = []string{"apply", score, "intents.json"}
	case "render":
		args = []string{"render", score, "-o", "out.wav"}
	case "loudness":
		args = []string{"render", score, "-o", "out.wav", "--loudness", strconv.FormatFloat(o.loudness, 'g', -1, 64)}
	case "audition":
		args = []string{"audition", score, "--json"}
	default:
		if jobs, ok := renderJobs(kind); ok && jobs == 0 {
			result.Auto = true
			args = []string{"render", score, "-o", "out.wav", "--jobs", "auto"}
		} else if ok {
			result.Jobs = jobs
			args = []string{"render", score, "-o", "out.wav", "--jobs", strconv.Itoa(jobs)}
		}
	}
	if result.Bars > 0 {
		args = append(args, "--bars", strconv.Itoa(result.Bars))
	}
	result.Args = args
	// The first run warms the file cache and is not counted.
	for run := range o.runs + 1 {
		started := time.Now()
		command := exec.Command(binary, args...)
		command.Dir = dir
		var stdout, stderr strings.Builder
		command.Stdout, command.Stderr = &stdout, &stderr
		err := command.Run()
		elapsed := time.Since(started)
		result.ExitCode = 0
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			result.ExitCode = exit.ExitCode()
		} else if err != nil {
			return result, err
		}
		// A loudness render that misses its target still writes its best pass
		// and exits 1; that is a result, not a failure of the benchmark.
		if result.ExitCode != 0 && !(kind == "loudness" && strings.Contains(stdout.String(), "passes")) {
			return result, fmt.Errorf("%s exited %d: %s", strings.Join(args, " "), result.ExitCode, lastLine(stderr.String()))
		}
		if run == 0 {
			continue
		}
		result.RunsMS = append(result.RunsMS, float64(elapsed.Microseconds())/1000)
		if command.ProcessState != nil {
			result.PeakRSSMB = max(result.PeakRSSMB, float64(peakRSS(command.ProcessState))/(1<<20))
		}
		readOutput(&result, kind, stdout.String())
		if kind == "render" || result.Jobs > 0 || result.Auto {
			sum, err := fileSHA256(filepath.Join(dir, "out.wav"))
			if err != nil {
				return result, err
			}
			if run > 1 && sum != result.SHA256 {
				result.UnstableHash = true
			}
			result.SHA256 = sum
		}
	}
	if result.UnstableHash {
		result.SHA256 = ""
	}
	sorted := slices.Clone(result.RunsMS)
	slices.Sort(sorted)
	result.MinMS, result.MaxMS = sorted[0], sorted[len(sorted)-1]
	result.MedianMS = median(sorted)
	if result.AudioSeconds > 0 && result.MedianMS > 0 {
		result.Realtime = result.AudioSeconds / (result.MedianMS / 1000)
	}
	return result, nil
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

var (
	framesPattern    = regexp.MustCompile(`(\d+) frames at (\d+) Hz`)
	passesPattern    = regexp.MustCompile(`passes (\d+)`)
	synthesesPattern = regexp.MustCompile(`full syntheses (\d+)`)
	replaysPattern   = regexp.MustCompile(`cached replays (\d+)`)
	repeatsPattern   = regexp.MustCompile(`repeats (\d+)`)
)

// readOutput takes the rendered length, and for a loudness render the pass and
// synthesis counts, from the summary line that render prints.
func readOutput(result *Result, kind, stdout string) {
	if _, jobs := renderJobs(kind); kind != "render" && kind != "loudness" && !jobs {
		return
	}
	if match := framesPattern.FindStringSubmatch(stdout); match != nil {
		frames, _ := strconv.ParseFloat(match[1], 64)
		rate, _ := strconv.ParseFloat(match[2], 64)
		if rate > 0 {
			result.AudioSeconds = frames / rate
		}
	}
	if kind != "loudness" {
		return
	}
	result.Passes = submatchInt(passesPattern, stdout, 0)
	result.Syntheses = submatchInt(synthesesPattern, stdout, -1)
	result.Replays = submatchInt(replaysPattern, stdout, 0)
	result.Repeats = submatchInt(repeatsPattern, stdout, 0)
}

func submatchInt(pattern *regexp.Regexp, text string, missing int) int {
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return missing
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		return missing
	}
	return value
}

// copyScore copies a score and the cicada.mod and cicada.sum beside it into
// dir, so the steps never write next to the repository's examples. It returns
// the copy's name in dir; the steps run in dir.
func copyScore(path, dir string) (string, error) {
	for _, name := range []string{"cicada.mod", "cicada.sum"} {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(path), name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	return name, os.WriteFile(filepath.Join(dir, name), data, 0o644)
}

var trackPattern = regexp.MustCompile(`(?m)^track ([A-Za-z_][A-Za-z0-9_-]*) `)

// writeIntents writes an envelope of count setparam intents that set the level
// of the score's tracks in turn, from -3 dB to -7 dB.
func writeIntents(score, path string, count int) error {
	source, err := os.ReadFile(score)
	if err != nil {
		return err
	}
	var tracks []string
	for _, match := range trackPattern.FindAllStringSubmatch(string(source), -1) {
		tracks = append(tracks, match[1])
	}
	if len(tracks) == 0 {
		return fmt.Errorf("%s declares no tracks", score)
	}
	type intent struct {
		Kind   string `json:"kind"`
		Entity string `json:"entity"`
		Value  string `json:"value"`
	}
	envelope := struct {
		Version int      `json:"version"`
		Intents []intent `json:"intents"`
	}{Version: 1}
	for i := range count {
		envelope.Intents = append(envelope.Intents, intent{Kind: "setparam", Entity: "param:" + tracks[i%len(tracks)] + ".level", Value: fmt.Sprintf("-%ddB", 3+i%5)})
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func formatResult(r Result) string {
	line := fmt.Sprintf("%-16s %-28s median %s  [%s to %s]", r.Step, r.Score, formatMS(r.MedianMS), formatMS(r.MinMS), formatMS(r.MaxMS))
	if r.Bars > 0 {
		line += fmt.Sprintf("  first %d bars", r.Bars)
	}
	if r.Realtime > 0 {
		line += fmt.Sprintf("  %.1fx real time (%.1f s of audio)", r.Realtime, r.AudioSeconds)
	}
	if r.Passes > 0 {
		syntheses := "not reported"
		if r.Syntheses >= 0 {
			syntheses = strconv.Itoa(r.Syntheses)
		}
		line += fmt.Sprintf("  passes %d, full syntheses %s, exit %d", r.Passes, syntheses, r.ExitCode)
	}
	if r.PeakRSSMB > 0 {
		line += fmt.Sprintf("  peak RSS %.0f MiB", r.PeakRSSMB)
	}
	switch {
	case r.UnstableHash:
		line += "  output differs between runs"
	case r.SHA256 != "" && (r.Jobs > 0 || r.Auto):
		line += "  sha256 " + r.SHA256[:16]
	}
	return line
}

func formatMS(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.2f s", ms/1000)
	}
	return fmt.Sprintf("%.1f ms", ms)
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return lines[len(lines)-1]
}

// cpuList names the processors this process, and so every run it starts, may
// use: the Linux affinity list (pin with taskset to choose them), or "all"
// where the host does not restrict them.
func cpuList() string {
	if data, err := os.ReadFile("/proc/self/status"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if list, ok := strings.CutPrefix(line, "Cpus_allowed_list:"); ok {
				return "CPUs " + strings.TrimSpace(list)
			}
		}
	}
	return "all"
}

// loadAverage returns the 1, 5 and 15 minute load averages, or "" where the
// host does not expose them.
func loadAverage() string {
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			return strings.Join(fields[:3], " ")
		}
	}
	if runtime.GOOS == "darwin" {
		if data, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
			return strings.Trim(strings.TrimSpace(string(data)), "{} ")
		}
	}
	return ""
}

// displayPath writes a path inside the working folder relative to it, with
// forward slashes, and any other path in full.
func displayPath(path string) string {
	wd, err := os.Getwd()
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(wd, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.ToSlash(relative)
}

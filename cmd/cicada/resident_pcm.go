package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// maxResidentPCMFlag names the option that caps the decoded sampler-pack PCM
// that a native command holds in memory. Without it the cap is
// instrumentpack.DefaultMaxResidentPCM, 1 GiB.
const maxResidentPCMFlag = "max-resident-pcm"

// maxResidentPCMEnv sets the same cap for a command that was not given the
// option, such as Studio started from a launcher that passes no flags.
const maxResidentPCMEnv = "CICADA_MAX_RESIDENT_PCM"

var byteUnits = map[string]int64{
	"": 1, "b": 1,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
	"kb": 1000, "mb": 1000 * 1000, "gb": 1000 * 1000 * 1000, "tb": 1000 * 1000 * 1000 * 1000,
}

// parseByteSize reads a size: a whole number of bytes with an optional unit.
// KiB, MiB, GiB and TiB are powers of 1024, KB, MB, GB and TB are powers of
// 1000, and no unit (or B) means bytes. Units ignore case. A size is positive.
func parseByteSize(text string) (int64, error) {
	spelled := strings.TrimSpace(text)
	digits, unit := spelled, ""
	if i := strings.IndexFunc(spelled, func(r rune) bool { return r < '0' || r > '9' }); i >= 0 {
		digits, unit = spelled[:i], strings.TrimSpace(spelled[i:])
	}
	count, err := strconv.ParseUint(digits, 10, 63)
	tooLarge := errors.Is(err, strconv.ErrRange)
	if digits == "" || err != nil && !tooLarge || strings.IndexFunc(unit, func(r rune) bool { return !unicode.IsLetter(r) }) >= 0 {
		return 0, fmt.Errorf("size %q is not a whole number of bytes with an optional unit, such as 512MiB or 1GiB", text)
	}
	multiplier, known := byteUnits[strings.ToLower(unit)]
	switch {
	case !known:
		return 0, fmt.Errorf("size %q has the unknown unit %q (use B, KiB, MiB, GiB or TiB)", text, unit)
	case tooLarge || int64(count) > math.MaxInt64/multiplier:
		return 0, fmt.Errorf("size %q is too large", text)
	case count == 0:
		return 0, fmt.Errorf("size %q must be more than zero", text)
	}
	return int64(count) * multiplier, nil
}

// parseMaxResidentPCM removes --max-resident-pcm and its value from a command's
// arguments, as audiobackend.ParseFlag does for --audio. The last occurrence
// wins. Without the option the cap is CICADA_MAX_RESIDENT_PCM, if it is set;
// without either it is zero, which selects the default.
func parseMaxResidentPCM(args []string) (rest []string, limit int64, err error) {
	rest = make([]string, 0, len(args))
	seen := false
	for i := 0; i < len(args); i++ {
		arg, value, found := args[i], "", false
		switch {
		case arg == "--"+maxResidentPCMFlag || arg == "-"+maxResidentPCMFlag:
			if i+1 >= len(args) {
				return nil, 0, fmt.Errorf("--%s needs a size, such as 512MiB or 1GiB", maxResidentPCMFlag)
			}
			i++
			value, found = args[i], true
		case strings.HasPrefix(arg, "--"+maxResidentPCMFlag+"="):
			value, found = strings.TrimPrefix(arg, "--"+maxResidentPCMFlag+"="), true
		case strings.HasPrefix(arg, "-"+maxResidentPCMFlag+"="):
			value, found = strings.TrimPrefix(arg, "-"+maxResidentPCMFlag+"="), true
		}
		if !found {
			rest = append(rest, arg)
			continue
		}
		if limit, err = parseByteSize(value); err != nil {
			return nil, 0, fmt.Errorf("--%s: %w", maxResidentPCMFlag, err)
		}
		seen = true
	}
	if !seen {
		if text := os.Getenv(maxResidentPCMEnv); strings.TrimSpace(text) != "" {
			if limit, err = parseByteSize(text); err != nil {
				return nil, 0, fmt.Errorf("%s: %w", maxResidentPCMEnv, err)
			}
		}
	}
	return rest, limit, nil
}

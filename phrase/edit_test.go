package phrase

import (
	"reflect"
	"strings"
	"testing"

	"m31labs.dev/cicada/notation"
)

func TestMutationsKeepLockedStepsAndSourceParity(t *testing.T) {
	for scale := Minor; scale <= Blues; scale++ {
		for seed := uint64(0); seed < 24; seed++ {
			params := DefaultParams()
			params.Scale, params.Seed, params.Structure = scale, seed, A
			base, err := Generate(params)
			if err != nil {
				t.Fatal(err)
			}
			original := base.Bars[0]
			for kind := NudgeDegree; kind <= Ratchet; kind++ {
				for _, locked := range []uint64{0, 0x5555, 0xffff} {
					params.Seed = seed + 100
					result, err := Mutate(params, original, []Op{{Kind: kind, Arg: 2}}, locked)
					if err != nil {
						if !strings.Contains(err.Error(), "CICADA-LOCKED") {
							t.Fatalf("scale=%d seed=%d op=%d: %v", scale, seed, kind, err)
						}
						continue
					}
					for i := uint8(0); i < original.Len; i++ {
						if locked&(uint64(1)<<i) != 0 && original.Steps[i] != result.Bars[0].Steps[i] {
							t.Fatalf("locked step changed: scale=%d seed=%d op=%d step=%d", scale, seed, kind, i)
						}
					}
					score, diagnostics := notation.Parse([]byte(result.Notation))
					for _, d := range diagnostics {
						if d.Severity == "error" {
							t.Fatalf("mutation source: %+v", d)
						}
					}
					compiled, err := compiledScenePattern(score, "a")
					if err != nil || compiled != result.Bars[0] {
						t.Fatalf("mutation source parity: %v", err)
					}
				}
			}
			if base.Bars[0] != original {
				t.Fatal("mutation changed its input")
			}
		}
	}
}

func TestEvolutionIsReproducibleAndValidated(t *testing.T) {
	params := DefaultParams()
	params.Structure = A
	base, err := Generate(params)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Evolve(params, base.Bars[0], 42, 0)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evolve(params, base.Bars[0], 42, 0)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("evolution changed: %v", err)
	}
	if _, err := Mutate(params, base.Bars[0], nil, 1<<40); err == nil {
		t.Fatal("accepted a lock outside the pattern")
	}
	unsupported := base.Bars[0]
	unsupported.Transpose = 1
	if _, err := Mutate(params, unsupported, nil, 0); err == nil {
		t.Fatal("silently discarded transposition")
	}
	unsupported = base.Bars[0]
	unsupported.Steps[0] &= ^(uint32(0x7f) << 14)
	if _, err := Mutate(params, unsupported, nil, 0); err == nil {
		t.Fatal("silently discarded probability")
	}
}

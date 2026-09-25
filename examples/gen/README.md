# Generator reference set

`seed-0000.cicada` through `seed-0023.cicada` are the 24 fixed M1 source
fixtures. Each uses the default generator settings with key A minor and the
number in its filename as the 64-bit generator seed. The files are formatted
through the Cicada grammar and must remain byte-equal to `phrase.Generate`.

`seed-4242.trace.json` records every draw for the same settings with seed
4242. The first eight raw values match the PCG32 vector in Hyphae spec 04.
The trace is a generated reference, not the illustrative A/B phrase from an
earlier draft. `go test ./phrase -run TestSeedFixturesAndReferenceTrace` checks
both the source set and trace.

To reproduce one source file:

```sh
go run ./cmd/cicada gen --seed 0 --key a --scale minor -o examples/gen/seed-0000.cicada
```

To reproduce the trace, use `--seed 4242 --key a --scale minor --trace -o
build/seed-4242.cicada` and save its stdout as the trace JSON.

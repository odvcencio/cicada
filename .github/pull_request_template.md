## Summary

<!-- What changes and why. Name the language constructs, engine paths, or commands it touches. -->

## Changes

<!-- One bullet per commit or area. Commit subjects follow `type(scope): description`,
     with type add, fix, update, document, or test. -->

-

## Verification

<!-- Check what ran. For anything that did not run, say why. -->

- [ ] `make grammar-check`: the generated parser and the highlight query
- [ ] `make test` and `go vet ./...`
- [ ] `make test-golden`
- [ ] `make test-alloc test-timing`
- [ ] `make test-kernel-wasm`
- [ ] `cicada validate` and `cicada fmt --check` on every example

<!-- Grammar change: regenerate notation/cicada.bin with the pinned grammargen, and say
     whether existing examples still parse to the same trees.
     Golden change: include the drift report from `cicada golden --update` and a listening
     note (docs/golden.md).
     Highlight change: include the language/testdata/*.spans diff from `go test ./language -update`. -->

## Known limits

<!-- What stays open, is documented instead of fixed, or is out of scope. Delete if none. -->

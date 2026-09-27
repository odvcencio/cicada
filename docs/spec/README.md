# Cicada language specification

This specification defines Cicada source edition 1, its typed project model, and the constructs approved for later implementation.

## Status labels

- **Implemented** means the construct is available on main and accepted by the validator.
- **Accepted** means the owner approved the design, but it is not available in the current build. Examples use cicada-accepted fences and are skipped by the documentation validator until the construct lands.
- **Proposed** means the design has not been approved.

Edition 1 is the only implemented source edition. See [edition 1](edition-1.md), the [semantic JSON mapping](semantic-model.md), [accepted syntax](accepted.md), and the [EBNF appendix](appendix.ebnf).

The Go test TestDocumentationCicadaExamples runs every cicada and cicada-invalid block in this directory and docs/manual through the same parser, semantic checks, and engine compilation used by cicada check. TestDocumentationEBNFProductionNamesMatchGrammar checks that the appendix has the same production names, in the same order, as the grammargen DSL in language/grammar/grammar.go.

The EBNF is a readable hand-maintained rendering of the grammargen DSL. The DSL also carries parser precedence, token priority, fields, and test cases, so the appendix omits those editor and parser-generation details. The production-name test prevents the appendix from silently losing or inventing a rule.


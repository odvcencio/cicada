; Cicada locals query.
;
; Instruments and performed patterns have lexical scopes. Parameters and ordered let
; bindings are visible to the let and out expressions that follow them, and a
; reference takes the highlight of the definition it resolves to. Tracks,
; patterns, phrases, and scenes live in separate file-wide namespaces that name
; lookup alone cannot tell apart; tags.scm records those with their kinds.

(instrument_decl) @local.scope
(performance_pattern) @local.scope
(performed_group name: (identifier) @local.definition.var)
(performed_note group: (identifier) @local.reference)

(instrument_param name: (identifier) @local.definition.parameter)
(let_stmt name: (identifier) @local.definition.var)

(expression (identifier) @local.reference)

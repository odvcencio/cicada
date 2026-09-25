; Cicada tags query.
;
; Tag kinds name the musical role of each symbol, so an outline or a
; go-to-definition tool can tell a track called `bass` from a pattern called
; `bass`. Built-in names (the acid and drums voices, the off and keep scene
; bindings, voice inputs) have no definition and are not tagged.

(instrument_decl name: (identifier) @name) @definition.instrument
(instrument_param name: (identifier) @name) @definition.parameter
(let_stmt name: (identifier) @name) @definition.binding
(track_decl name: (identifier) @name) @definition.track
(fx_decl name: (identifier) @name) @definition.effect
(phrase_decl name: (identifier) @name) @definition.phrase
(acid_pattern name: (identifier) @name) @definition.pattern
(note_pattern name: (identifier) @name) @definition.pattern
(drum_pattern name: (identifier) @name) @definition.pattern
(scene_decl name: (identifier) @name) @definition.scene

((track_decl kind: (identifier) @name) @reference.instrument
  (#not-any-of? @name "acid" "drums"))
(scene_assignment track: (identifier) @name) @reference.track
((scene_assignment pattern: (identifier) @name) @reference.pattern
  (#not-any-of? @name "off" "keep"))
(phrase_use name: (identifier) @name) @reference.phrase
(song_entry scene: (identifier) @name) @reference.scene
((expression (identifier) @name) @reference.binding
  (#not-any-of? @name "pitch" "gate" "velocity" "sample_rate"))

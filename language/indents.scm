; Cicada indents query: braces open a level and a closing brace returns to the
; level of its declaration, matching `cicada fmt`.

[
  (instrument_decl)
  (kit_decl)
  (voice_decl)
  (track_decl)
  (fx_decl)
  (phrase_decl)
  (acid_pattern)
  (note_pattern)
  (drum_pattern)
  (scene_decl)
  (song_decl)
] @indent.begin

"}" @indent.end
"}" @indent.branch

(comment) @indent.auto

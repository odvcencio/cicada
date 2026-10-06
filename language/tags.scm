; Cicada tags query.
;
; Tag kinds name the musical role of each symbol, so an outline or a
; go-to-definition tool can tell a track called `bass` from a pattern called
; `bass`. Built-in names (the acid and drums voices, the off and keep scene
; bindings, voice inputs) have no definition and are not tagged.

(instrument_decl name: (identifier) @name) @definition.instrument
(kit_decl name: (identifier) @name) @definition.kit
(instrument_param name: (identifier) @name) @definition.parameter
(let_stmt name: (identifier) @name) @definition.binding
(track_decl name: (identifier) @name) @definition.track
(fx_decl name: (identifier) @name) @definition.effect
(bus_decl name: (identifier) @name) @definition.bus
(export_decl name: (identifier) @name) @definition.export
(phrase_decl name: (identifier) @name) @definition.phrase
(acid_pattern name: (identifier) @name) @definition.pattern
(note_pattern name: (identifier) @name) @definition.pattern
(drum_pattern name: (identifier) @name) @definition.pattern
(scene_decl name: (identifier) @name) @definition.scene

((track_decl kind: (identifier) @name) @reference.voice
  (#not-any-of? @name "acid" "drums" "audio"))
(kit_target instrument: (identifier) @name) @reference.instrument
(scene_assignment target: (scene_target (identifier) @name)) @reference.track
((scene_assignment value: (scene_value (identifier) @name)) @reference.pattern
  (#not-any-of? @name "off" "keep"))
(phrase_use name: (identifier) @name) @reference.phrase
(song_entry scene: (identifier) @name) @reference.scene
((send_decl to: (identifier) @name) @reference.mixer)
(insert_chain first: (identifier) @name) @reference.mixer
(insert_chain next: (identifier) @name) @reference.mixer
((param_decl name: (identifier) @field value: (value (identifier) @name)) @reference.mixer
  (#any-of? @field "insert" "out" "bus"))
((expression (identifier) @name) @reference.binding
  (#not-any-of? @name "pitch" "gate" "velocity" "sample_rate"))

(asset_decl name: (identifier) @name) @definition.asset
(clip_decl name: (identifier) @name) @definition.clip
(clip_decl asset: (identifier) @name) @reference.asset
(sampler_decl name: (identifier) @name) @definition.sampler

((sampler_decl (param_decl name: (identifier) @field value: (value (identifier) @name))) @reference.asset
  (#eq? @field "asset"))

(track_decl kind: (qualified_name) @name) @reference.voice
(kit_target instrument: (qualified_name) @name) @reference.instrument
(scene_assignment value: (scene_value (qualified_name) @name)) @reference.pattern
(phrase_use name: (qualified_name) @name) @reference.phrase
(send_decl to: (qualified_name) @name) @reference.mixer
(insert_chain first: (qualified_name) @name) @reference.mixer
(insert_chain next: (qualified_name) @name) @reference.mixer
(clip_decl asset: (qualified_name) @name) @reference.asset
((param_decl name: (identifier) @field value: (value (qualified_name) @name)) @reference.mixer
  (#any-of? @field "insert" "out" "bus"))
((sampler_decl (param_decl name: (identifier) @field value: (value (qualified_name) @name))) @reference.asset
  (#eq? @field "asset"))

(live_state name: (identifier) @name) @definition.state
(live_state scene: (identifier) @name) @reference.scene
(live_stinger name: (identifier) @name) @definition.stinger
(live_stinger track: (identifier) @name) @reference.track
(live_stinger pattern: (identifier) @name) @reference.pattern
(live_transition from: (identifier) @name) @reference.state
(live_transition to: (identifier) @name) @reference.state
(preset_decl name: (identifier) @name) @definition.preset
(preset_decl name: (qualified_name) @name) @definition.preset

((preset_decl (param_decl name: (identifier) @field value: (value (identifier) @name))) @reference.preset-target
  (#eq? @field "instrument"))
((preset_decl (param_decl name: (identifier) @field value: (value (qualified_name) @name))) @reference.preset-target
  (#eq? @field "instrument"))

((fx_decl kind: (identifier) @name) @reference.preset
  (#not-any-of? @name "delay" "reverb" "drive" "comp"))
(fx_decl kind: (qualified_name) @name) @reference.preset

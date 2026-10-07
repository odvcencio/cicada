package engine

// Fault codes.
//
// A fault stops the engine and reports its code in the A field of a cmd.Fault
// message. Each code below names one meaning. Hosts and logs match on the
// number, so give every code an explicit number, never renumber a code that has
// shipped, and never reuse a retired number.
//
// Codes 20, 21 and 22 are declared in kernel/cmd, because hosts import them from
// there: cmd.FaultPolyLive, cmd.FaultDirectorQuantize and cmd.FaultDirectorStinger.
//
// Numbers 12 to 19 are retired. Before this list existed, each of them was
// shared by two to five unrelated faults, so a log line that holds one of them
// cannot be decoded with certainty. They are not reused. An old build reported
// one of these meanings for each number:
//
//	12  FaultDriveInsert, FaultHostCommandBatch
//	13  FaultDelayTempo, FaultHostRenderFrames
//	14  FaultDelay, FaultQuantize
//	15  FaultReverb, FaultPatternEdit, FaultScheduledNote
//	16  FaultCompressor, FaultPatternEvents
//	17  FaultSceneIndex, FaultClipStart, FaultChordUnsupported, FaultPolyNote,
//	    FaultPolyGeneration
//	18  FaultParam, FaultNoSharedPatternEnd
//	19  FaultMasterProcessor, FaultPreparedVoice, FaultChainPosition
//
// faults_test.go checks that no two codes in this file or in kernel/cmd share a
// number, that no code reuses a retired number, and that engine code passes
// these names, not number literals, to fault.
const (
	// FaultRenderBlock: a render call got slices that are empty, differ in
	// length, or exceed the largest block.
	FaultRenderBlock uint16 = 1
	// FaultAcidVoice: the acid voice reported a fault.
	FaultAcidVoice uint16 = 2
	// FaultDrumVoice: the drum voice reported a fault.
	FaultDrumVoice uint16 = 3
	// FaultLimiter: the master limiter reported a fault.
	FaultLimiter uint16 = 4
	// FaultPendingFull: the queue of commands that wait for a future tick is full.
	FaultPendingFull uint16 = 5
	// FaultSeek: the transport rejected a seek.
	FaultSeek uint16 = 6
	// FaultTempo: the transport rejected a tempo change.
	FaultTempo uint16 = 7
	// FaultNoteRange: a live note-on or note-off named a pitch or drum lane that
	// the track's voice rejects.
	FaultNoteRange uint16 = 8
	// FaultVoiceUnsupported: the track's voice cannot take this command or note.
	// The track is off, its voice kind lacks the feature, or the sampler cannot
	// pitch its sample to the note.
	FaultVoiceUnsupported uint16 = 9
	// FaultUnhandledCommand: apply received a command that it has no case for.
	// Validation must refuse every command that apply cannot run.
	FaultUnhandledCommand uint16 = 10
	// FaultMessageOverflow: the status queue is full of messages that cannot be
	// dropped or merged.
	FaultMessageOverflow uint16 = 11

	// FaultDriveInsert: a track's drive insert reported a fault.
	FaultDriveInsert uint16 = 23
	// FaultDelayTempo: the delay send could not follow a tempo change.
	FaultDelayTempo uint16 = 24
	// FaultDelay: the delay send reported a fault.
	FaultDelay uint16 = 25
	// FaultReverb: the reverb return reported a fault.
	FaultReverb uint16 = 26
	// FaultCompressor: the music-bus compressor reported a fault.
	FaultCompressor uint16 = 27
	// FaultMasterProcessor: the master processor reported a fault.
	FaultMasterProcessor uint16 = 28
	// FaultPreparedVoice: a prepared voice rejected a play, a slot selection or a
	// note.
	FaultPreparedVoice uint16 = 29

	// FaultParam: a parameter command or scene setting could not be applied. The
	// parameter is unknown or not live, the value is out of range, the track or
	// voice does not own it, or a processor refused the new value.
	FaultParam uint16 = 30
	// FaultQuantize: a scene launch or pattern switch carried a quantize value
	// that the sequencer rejects.
	FaultQuantize uint16 = 31
	// FaultSceneIndex: a scene launch or state change named a scene that does
	// not exist.
	FaultSceneIndex uint16 = 32
	// FaultNoSharedPatternEnd: a scene launch at pattern end found no tick where
	// all active patterns end together.
	FaultNoSharedPatternEnd uint16 = 33
	// FaultClipStart: a clip could not start. The clip is unknown, the clip voice
	// rejected the note, or no clip voice was free.
	FaultClipStart uint16 = 34
	// FaultChainPosition: a chain entry was written past the end of the chain.
	FaultChainPosition uint16 = 35

	// FaultPatternEdit: a step, chord, length or metadata command would make a
	// pattern invalid.
	FaultPatternEdit uint16 = 36
	// FaultPatternEvents: a pattern produced more events in one block than the
	// track can hold.
	FaultPatternEvents uint16 = 37
	// FaultScheduledNote: the track's voice rejected a note that a pattern
	// scheduled. A piano or keys note-on failed, or a drum hit named a lane that
	// does not exist.
	FaultScheduledNote uint16 = 38
	// FaultChordUnsupported: a chord step went to a track whose voice has no
	// chord support.
	FaultChordUnsupported uint16 = 39
	// FaultPolyNote: the polyphonic voice pool rejected a pattern note.
	FaultPolyNote uint16 = 40
	// FaultPolyGeneration: a polyphonic track ran out of pattern generations.
	// Wrapping would reuse an old note identity.
	FaultPolyGeneration uint16 = 41

	// FaultHostCommandBatch: the host refused a command batch before the engine
	// saw it. The count was out of range, a record was invalid, or the queue was
	// full. A host reports it with InjectFault.
	FaultHostCommandBatch uint16 = 42
	// FaultHostRenderFrames: the host asked for a render of a frame count outside
	// 1 to the block limit. A host reports it with InjectFault.
	FaultHostRenderFrames uint16 = 43
)

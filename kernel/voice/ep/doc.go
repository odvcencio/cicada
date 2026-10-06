// Tine and Reed are original electromechanical designs. Tine uses a tuned
// tine/tonebar modal pair plus inharmonic bending modes. Reed uses the
// clamped-free beam ratios. Hammer felt and strike velocity set modal
// excitation; displacement enters a nonlinear magnetic or electrostatic
// pickup. Pickup voltage is differentiated flux/capacitance. A finite reed
// stop prevents a singular plate gap. Damper contact adds a damped 54 Hz
// thump and deterministic contact noise. Shared preamp coloration, tremolo
// and stereo auto-pan follow the voices.
//
// Physical controls are design units, not calibrated millimeters. The models
// do not claim reproduction of a particular instrument or commercial patch.
// No recordings, factory data or third-party audio are embedded.
//
// Parameters: PickupPosition [0,1], PickupDistance [.2,2], HammerFelt [0,1],
// Decay [.3,20] seconds T60 at middle C, Release [.02,2] seconds T60,
// Drive/Tremolo/Pan [0,1], TremoloRate [.1,12] Hz, Gain [0,2]. Oversample is
// 2 or 4; offline sampling can use 192 kHz and four substeps. Modes above
// .42 times output rate are excluded; an output low-pass bounds coloration.
// New allocates and prepares all 88 keys. NoteOn, NoteOff, AllNotesOff,
// SetSustain, Reset and NextStereo allocate nothing. Eight voices use oldest
// voice stealing with a short residual fade. A single owner serializes calls.
//
// The ep_wasm gate compares float32 PCM bits at four sample rates and three
// block sizes and checks TinyGo allocation telemetry. Golden tests pin note,
// retrigger and release output. Engine/score integration is a separate layer.
package ep

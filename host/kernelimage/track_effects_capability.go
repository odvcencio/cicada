package kernelimage

// TrackEffectsCapability adds bounded, ordered EQ/comp/de-esser/drive records
// after the legacy insert record. Images without it remain byte-identical.
const TrackEffectsCapability uint16 = 1 << 11

// AmpAutomationCapability requires append-only track-owned amp control IDs.
const AmpAutomationCapability uint16 = 1 << 12

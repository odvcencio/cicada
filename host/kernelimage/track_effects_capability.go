package kernelimage

// TrackEffectsCapability adds bounded, ordered EQ/comp/de-esser/drive records
// after the legacy insert record. Images without it remain byte-identical.
const TrackEffectsCapability uint16 = 1 << 11

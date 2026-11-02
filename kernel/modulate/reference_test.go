package modulate

// Pinned outputs. The sample-hold values were also computed by an independent
// SplitMix64 script from the spec's formula (seed 4242, domain 1, track 0xFF,
// slot 0, step 0, index k); the sway values pin this implementation.
// The digest lives in testdata/fixture.
const (
	referenceSampleHold = "-1003774223 -893066423 679043583 -120233633 -295812813 -246762029 -203615227 -968377649 -248910397 1010851059 1017104623 716365693 739111733 -181627521 860848601 905430049"
	referenceSway       = "-901580503 -737030420 -435368283 75457363 449170955 181111990 -240208775 -496051372 -598632166 -549760192 -315131449 262288018 653950718 355451581 24550120 88679137"
)

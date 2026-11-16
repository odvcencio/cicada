package scoreaudition

// DropDetail removes the detail that cicada audition --json writes only with
// --detail, so the default document stays small enough for an agent to read
// whole: bars[].sample_peak_dbfs, sounding and bands; tracks[].bands_dbfs,
// bar_level_db, sounding, bar_bands_dbfs and section_bands_dbfs;
// sections[].true_peak_dbtp, band_collision_bar_count and bands;
// loudness.short_term_per_beat_lufs;
// and bands.collisions[].bars. The summaries stay: loudness (with
// short_term_per_bar_lufs), tracks[].sounding_spans and repeat counts,
// sections[] loudness and band_owners, bands.collisions[].pairs, activity and
// peaks.
func (r *Report) DropDetail() {
	for i := range r.Bars {
		r.Bars[i].SamplePeakDBFS, r.Bars[i].Sounding, r.Bars[i].Bands = nil, nil, nil
	}
	for i := range r.Tracks {
		t := &r.Tracks[i]
		t.BandsDBFS, t.BarLevelDB, t.Sounding, t.BarBandsDBFS, t.SectionBandsDBFS = nil, nil, nil, nil, nil
	}
	for i := range r.Sections {
		r.Sections[i].TruePeakDBTP, r.Sections[i].BandCollisionBarCount, r.Sections[i].Bands = nil, nil, nil
	}
	if r.Loudness != nil {
		r.Loudness.ShortTermPerBeatLUFS = nil
	}
	if r.Bands != nil {
		for i := range r.Bands.Collisions {
			r.Bands.Collisions[i].Bars = nil
		}
	}
}

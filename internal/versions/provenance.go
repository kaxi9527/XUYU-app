package versions

func appendSourceClaims(c *Catalog, versionID string, claims []SourceClaim) error {
	for _, claim := range claims {
		found := false
		for _, existing := range c.Provenance {
			if existing.VersionID == versionID && existing.Kind == claim.Kind && existing.Tool == claim.Tool && existing.Agent == claim.Agent && existing.Recorder == claim.Recorder && existing.Task == claim.Task && existing.Source == claim.Source && existing.Parameters == claim.Parameters && existing.ClaimedAt == claim.ClaimedAt && existing.Evidence == claim.Evidence && existing.Binding == claim.Binding {
				found = true
				break
			}
		}
		if found {
			continue
		}
		id, err := newID("provenance")
		if err != nil {
			return err
		}
		c.Provenance = append(c.Provenance, Provenance{ID: id, VersionID: versionID, Kind: claim.Kind, Tool: claim.Tool, Agent: claim.Agent, Recorder: claim.Recorder, Task: claim.Task, Source: claim.Source, Parameters: claim.Parameters, ClaimedAt: claim.ClaimedAt, Evidence: claim.Evidence, Binding: claim.Binding, RecordedAt: nowString()})
	}
	return nil
}

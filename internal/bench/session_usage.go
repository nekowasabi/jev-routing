package bench

func classifySessions(run *RunRecord, mainKey string) {
	if run == nil || mainKey == "" || run.UnattributedRequests != 0 {
		return
	}
	if _, ok := run.SessionUsage[mainKey]; !ok {
		return
	}
	run.MainSessionKey = mainKey
	run.ParentChildVerified = true
	for key, session := range run.SessionUsage {
		if key == mainKey {
			continue
		}
		run.ChildSessions++
		input := session.Input + session.CacheWrite
		if !specOf(run.Agent).inputIncludesCached {
			input += session.Cached
		}
		run.ChildTokens += input + session.Output + session.JevInput + session.JevOutput
	}
}

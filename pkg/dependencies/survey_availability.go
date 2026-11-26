package dependencies

func surveyAvailability(observed LookupResult) string {
	availability := surveyMissing
	if observed.ReasonCode == LookupInvalidPath && len(observed.Candidates) == 0 {
		availability = surveyUnknown
	}
	for _, candidate := range observed.Candidates {
		state := surveyFileAvailability(candidate.State)
		if state == surveyAvailable {
			return state
		}
		if state == surveyUnknown || availability == surveyMissing {
			availability = state
		}
	}
	return availability
}

func surveyFileAvailability(state FileState) string {
	switch state {
	case FileExecutable:
		return surveyAvailable
	case FileMissing:
		return surveyMissing
	case FileBrokenLink, FileNonregular, FileNonexecutable:
		return surveyIncomplete
	default:
		return surveyUnknown
	}
}

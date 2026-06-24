package core

// MergeLinks merges link values into an existing link map, returning a new map
// without mutating the input. Each of docs, issues and source may be a string,
// a []string or a StringList; websites is always a plain []string. Duplicate
// values within a key are collapsed via UniqueAppend.
func MergeLinks(existing map[string][]string, docs, issues, source any, websites []string) map[string][]string {
	out := map[string][]string{}
	for key, values := range existing {
		out[key] = append([]string(nil), values...)
	}
	for _, doc := range LinkValues(docs) {
		out["docs"] = UniqueAppend(out["docs"], doc)
	}
	for _, issue := range LinkValues(issues) {
		out["issues"] = UniqueAppend(out["issues"], issue)
	}
	for _, sourceLink := range LinkValues(source) {
		out["source"] = UniqueAppend(out["source"], sourceLink)
	}
	for _, website := range websites {
		out["website"] = UniqueAppend(out["website"], website)
	}
	return out
}

// LinkValues normalizes a link field that may be a string, a []string or a
// StringList into a plain []string. Empty strings yield nil.
func LinkValues(raw any) []string {
	switch value := raw.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []string{value}
	case []string:
		return value
	case StringList:
		return []string(value)
	default:
		return nil
	}
}

// UniqueAppend appends candidate to values only if it is not already present.
func UniqueAppend(values []string, candidate string) []string {
	for _, value := range values {
		if value == candidate {
			return values
		}
	}
	return append(values, candidate)
}

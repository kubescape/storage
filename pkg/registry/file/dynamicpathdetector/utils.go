package dynamicpathdetector

func MergeStrings(existing, new []string) []string {
	methodSet := make(map[string]bool)
	for _, m := range existing {
		methodSet[m] = true
	}
	for _, m := range new {
		if !methodSet[m] {
			existing = append(existing, m)
			methodSet[m] = true
		}
	}

	return existing
}

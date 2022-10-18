package collections

// Index returns the first index of the target string t, or -1 if no match is found.
func Index(vs []string, t string) int {
	for i, v := range vs {
		if v == t {
			return i
		}
	}
	return -1
}

// Include returns true if the target string t is in the slice.
func Include(vs []string, t string) bool {
	return Index(vs, t) >= 0
}

// Any returns true if one of the strings in the slice satisfies the predicate f.
func Any(vs []string, f func(string) bool) bool {
	for _, v := range vs {
		if f(v) {
			return true
		}
	}
	return false
}

// All returns true if all of the strings in the slice satisfy the predicate f.
func All(vs []string, f func(string) bool) bool {
	for _, v := range vs {
		if !f(v) {
			return false
		}
	}
	return true
}

// Filter returns a new slice containing all strings in the slice that satisfy the predicate f.
func Filter(vs []string, f func(string) bool) []string {
	vsf := make([]string, 0)
	for _, v := range vs {
		if f(v) {
			vsf = append(vsf, v)
		}
	}
	return vsf
}

// Intersection of 2 slices
func Intersection(a, b []string) (c []string) {
	m := make(map[string]bool)

	for _, item := range a {
		m[item] = true
	}

	for _, item := range b {
		if _, ok := m[item]; ok {
			c = append(c, item)
		}
	}
	return
}

// Remove returns the slice after removing t from it
func Remove(vs []string, t string) []string {
	idx := Index(vs, t)
	if idx >= 0 {
		if idx != len(vs)-1 {
			copy(vs[idx:], vs[idx+1:])
		}
		vs[len(vs)-1] = ""
		return vs[:len(vs)-1]
	}
	return vs
}

// Difference returns the elements in `a` that aren't in `b`.
func Difference(a, b []string) []string {
	mb := make(map[string]struct{}, len(b))
	for _, x := range b {
		mb[x] = struct{}{}
	}
	var diff []string
	for _, x := range a {
		if _, found := mb[x]; !found {
			diff = append(diff, x)
		}
	}
	return diff
}

// RemoveDuplications returns a new distinct elements slice, using duplicateStringsList's elements, while preserving their order.
func RemoveDuplications(duplicateStringsList []string) []string {
	noDupsDict := make(map[string]bool)
	noDupsList := []string{}
	for _, possiblyDuplicatedString := range duplicateStringsList {
		_, found := noDupsDict[possiblyDuplicatedString]
		if !found {
			noDupsList = append(noDupsList, possiblyDuplicatedString)
			noDupsDict[possiblyDuplicatedString] = true
		}
	}
	return noDupsList
}

// Equal returns true if both lists contains the excat same strings
func Equal(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for _, stringInA := range a {
		found := Include(b, stringInA)
		if !found {
			return false
		}
	}
	return true
}

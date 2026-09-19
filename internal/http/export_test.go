package http

// Operations exposes the role table to the external tests.
func Operations() map[string]int {
	out := make(map[string]int, len(operations))
	for op, a := range operations {
		out[op] = int(a)
	}
	return out
}

// Access levels, for the external tests.
const (
	Public     = int(public)
	Reader     = int(reader)
	Member     = int(member)
	BundleRead = int(bundleRead)
	BundleAI   = int(bundleAI)
	BundleEdit = int(bundleEdit)
	AdminOnly  = int(adminOnly)
)

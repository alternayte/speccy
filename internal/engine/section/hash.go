package section

// HashAt returns the hash of the section with path in doc: its own content, as SDD §8.1 says.
// An empty path is the whole doc after the frontmatter, so a doc-level waiver ends on any edit.
// ok is false when no section has the path.
func HashAt(doc Doc, src []byte, path []string) (string, bool) {
	if len(path) == 0 {
		return Hash(src[doc.BodyStart:]), true
	}
	for _, s := range doc.Sections {
		if s.Level > 0 && equal(s.Path, path) {
			return s.Hash, true
		}
	}
	return "", false
}

// RangeAt returns the byte range of the section with path in doc: the heading line down to the
// end of its own content, which is the text the hash covers. An empty path is the whole doc
// after the frontmatter. ok is false when no section has the path.
func RangeAt(doc Doc, src []byte, path []string) (start, end int, ok bool) {
	if len(path) == 0 {
		return doc.BodyStart, len(src), true
	}
	for _, s := range doc.Sections {
		if s.Level > 0 && equal(s.Path, path) {
			return s.Start, s.OwnEnd, true
		}
	}
	return 0, 0, false
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

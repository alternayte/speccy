package anchor

import (
	"bytes"
	"strings"
	"unicode"
)

// Find returns the byte range of quote in src. It tries an exact match, then a match that
// ignores differences in whitespace, and markdown emphasis marks. A quote shorter than
// 4 characters never matches: it would match almost anywhere.
func Find(src []byte, quote string) (start, end int, ok bool) {
	q := strings.TrimSpace(quote)
	if len([]rune(q)) < 4 {
		return 0, 0, false
	}
	if i := bytes.Index(src, []byte(q)); i >= 0 {
		return i, i + len(q), true
	}
	// Normalized search: collapse whitespace runs and drop *, _, and ` on both sides, with a
	// map from each normalized byte back to its source offset.
	norm, pos := normalize(src)
	nq, _ := normalize([]byte(q))
	if len(nq) == 0 {
		return 0, 0, false
	}
	i := bytes.Index(norm, nq)
	if i < 0 {
		return 0, 0, false
	}
	return pos[i], pos[i+len(nq)-1] + 1, true
}

func normalize(src []byte) ([]byte, []int) {
	out := make([]byte, 0, len(src))
	pos := make([]int, 0, len(src))
	space := false
	for i, r := range string(src) {
		switch {
		case r == '*' || r == '_' || r == '`':
			continue
		case unicode.IsSpace(r):
			if !space && len(out) > 0 {
				out = append(out, ' ')
				pos = append(pos, i)
			}
			space = true
			continue
		}
		space = false
		n := len(string(r))
		for k := 0; k < n; k++ {
			out = append(out, src[i+k])
			pos = append(pos, i+k)
		}
	}
	if len(out) > 0 && out[len(out)-1] == ' ' {
		out, pos = out[:len(out)-1], pos[:len(pos)-1]
	}
	return out, pos
}

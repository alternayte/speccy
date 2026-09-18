package anchor

import "testing"

func TestFind(t *testing.T) {
	src := []byte("The **client** retries the\nrequest up to  three times.")
	for _, c := range []struct {
		quote string
		want  string
		ok    bool
	}{
		{"retries the", "retries the", true},
		{"client retries the request up to three times", "client** retries the\nrequest up to  three times", true},
		{"The client never retries", "", false},
		{"the", "", false},
	} {
		s, e, ok := Find(src, c.quote)
		if ok != c.ok || (ok && string(src[s:e]) != c.want) {
			t.Errorf("Find(%q) = %q, %v; want %q, %v", c.quote, src[s:e], ok, c.want, c.ok)
		}
	}
}

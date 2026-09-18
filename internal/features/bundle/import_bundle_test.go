package bundle

import (
	"archive/zip"
	"bytes"
	"testing"
)

func zipOf(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnzip(t *testing.T) {
	files, top, err := unzip(zipOf(t, "pay/SPEC.md", "pay/assets/a.png", "__MACOSX/pay/._SPEC.md", "pay/.DS_Store"))
	if err != nil {
		t.Fatal(err)
	}
	if top != "pay" || len(files) != 2 || files[0].Path != "SPEC.md" || files[1].Path != "assets/a.png" {
		t.Errorf("top %q, files %+v", top, files)
	}
	for _, evil := range []string{"../evil.md", "/abs.md", "a/../../evil.md"} {
		if _, _, err := unzip(zipOf(t, "SPEC.md", evil)); err == nil {
			t.Errorf("a zip entry %q was accepted", evil)
		}
	}
}

package http

import (
	"testing"
)

// T-040
func TestLocalMode_LoopbackOnly(t *testing.T) {
	refused := []string{":0", "0.0.0.0:0", "[::]:0", "192.168.1.10:0", "example.com:0"}
	for _, addr := range refused {
		if ln, err := ListenLocal(addr); err == nil {
			ln.Close()
			t.Errorf("ListenLocal(%q) listened; want an error", addr)
		}
	}
	allowed := []string{"127.0.0.1:0", "localhost:0"}
	for _, addr := range allowed {
		ln, err := ListenLocal(addr)
		if err != nil {
			t.Errorf("ListenLocal(%q): %v", addr, err)
			continue
		}
		ln.Close()
	}
}

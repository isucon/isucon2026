package isureal

import "testing"

// An image keeps the signatures of the current and the next expiry; a third
// replaces the older one.
func TestSignaturesKeepTwoExpiries(t *testing.T) {
	var s signatures
	a, b, c := [32]byte{1}, [32]byte{2}, [32]byte{3}
	s.set(100, a)
	s.set(200, b)
	if got, ok := s.get(100); !ok || got != a {
		t.Fatal("first expiry lost")
	}
	if got, ok := s.get(200); !ok || got != b {
		t.Fatal("second expiry lost")
	}
	s.set(300, c)
	if s.has(100) {
		t.Fatal("the older expiry was kept")
	}
	if got, ok := s.get(200); !ok || got != b {
		t.Fatal("the newer expiry was replaced")
	}
	if got, ok := s.get(300); !ok || got != c {
		t.Fatal("third expiry missing")
	}
}

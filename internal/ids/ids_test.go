package ids

import "testing"

func TestDocID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := DocID()
		if !ValidDocID(id) {
			t.Fatalf("invalid id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestValidDocID(t *testing.T) {
	for _, bad := range []string{"", "short", "abcdefghijk-", "abcdefghijklm", "../etc/passw"} {
		if ValidDocID(bad) {
			t.Errorf("ValidDocID(%q) = true", bad)
		}
	}
}

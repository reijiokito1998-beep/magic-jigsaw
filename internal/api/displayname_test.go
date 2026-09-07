package api

import (
	"strings"
	"testing"
)

func TestRandomDisplayNameShape(t *testing.T) {
	t.Parallel()

	for i := 0; i < 500; i++ {
		name := randomDisplayName()
		if len(name) != displayNameLen {
			t.Fatalf("randomDisplayName() = %q, want length %d", name, displayNameLen)
		}
		for _, c := range []byte(name) {
			if !strings.ContainsRune(displayNameAlphabet, rune(c)) {
				t.Fatalf("randomDisplayName() = %q contains %q, outside the alphabet", name, c)
			}
		}
	}
}

// A name derived from the email was the old behaviour; a generated one must be
// independent of it, and different for every account.
func TestRandomDisplayNameIsFresh(t *testing.T) {
	t.Parallel()

	const draws = 200
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		seen[randomDisplayName()] = struct{}{}
	}
	if len(seen) != draws {
		t.Errorf("got %d distinct names out of %d draws, want all distinct", len(seen), draws)
	}
}

// Every character class must be reachable in every position — a generator that
// only ever emitted, say, lower case would still pass the shape test.
func TestRandomDisplayNameUsesEveryClass(t *testing.T) {
	t.Parallel()

	var lower, upper, digit bool
	for i := 0; i < 500 && !(lower && upper && digit); i++ {
		for _, c := range randomDisplayName() {
			switch {
			case c >= 'a' && c <= 'z':
				lower = true
			case c >= 'A' && c <= 'Z':
				upper = true
			case c >= '0' && c <= '9':
				digit = true
			}
		}
	}
	if !lower || !upper || !digit {
		t.Errorf("lower=%v upper=%v digit=%v, want all true", lower, upper, digit)
	}
}

package api

import "crypto/rand"

// Display names are generated, never chosen: registration collects only an
// email and a password, so the server gives each new account a random handle
// before it is stored. It is what other players see (profile, leaderboards),
// which is exactly why it must not be derived from the email address the way it
// used to be — "user@corp.com" registering should not publish "user".
const (
	// displayNameLen is also the maximum: the client renders names in fixed
	// width slots, so every generated name is the same length.
	displayNameLen = 8

	displayNameAlphabet = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"0123456789"

	// Largest multiple of the alphabet size that fits in a byte. Drawing only
	// below it keeps every character equally likely; taking b%62 over the full
	// 0..255 range would favour the first four letters.
	displayNameCutoff = byte(256 - 256%len(displayNameAlphabet))
)

// randomDisplayName returns a fresh name of displayNameLen alphanumeric
// characters — lower case, upper case and digits all possible in any position.
//
// Names are not unique and carry no uniqueness constraint; with 62^8 (~2.2e14)
// possibilities a clash is a curiosity, not a conflict, and the account is
// identified by its id and email regardless.
func randomDisplayName() string {
	name := make([]byte, 0, displayNameLen)
	buf := make([]byte, displayNameLen)
	for len(name) < displayNameLen {
		// crypto/rand.Read fills buf entirely and never returns an error.
		_, _ = rand.Read(buf)
		for _, b := range buf {
			if b >= displayNameCutoff {
				continue // biased tail — redraw this character
			}
			name = append(name, displayNameAlphabet[int(b)%len(displayNameAlphabet)])
			if len(name) == displayNameLen {
				break
			}
		}
	}
	return string(name)
}

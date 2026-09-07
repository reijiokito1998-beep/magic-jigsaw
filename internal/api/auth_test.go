package api

import (
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"already normal", "user@example.com", "user@example.com"},
		{"upper case", "User@Example.COM", "user@example.com"},
		{"surrounding space", "  user@example.com\t", "user@example.com"},
		{"empty", "", ""},
		{"only space", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeEmail(tt.input); got != tt.want {
				t.Errorf("normalizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		email string
		want  bool
	}{
		// --- accepted ---
		{"plain", "user@example.com", true},
		{"subdomain", "user@mail.example.com", true},
		{"plus tag", "user+tag@example.com", true},
		{"dots in local", "first.last@example.com", true},
		{"hyphen in domain", "user@my-domain.com", true},
		{"digits", "user123@example123.com", true},
		{"long tld", "user@example.technology", true},
		{"two-letter tld", "user@example.vn", true},

		// --- shape ---
		{"empty", "", false},
		{"no at", "userexample.com", false},
		{"no local", "@example.com", false},
		{"no domain", "user@", false},
		{"two at signs", "user@@example.com", false},
		{"space inside", "user name@example.com", false},

		// --- domain must be a dotted hostname ---
		// The old validator accepted every one of these.
		{"bare hostname", "user@localhost", false},
		{"trailing dot", "user@example.", false},
		{"leading dot", "user@.example.com", false},
		{"double dot", "user@example..com", false},
		{"one-letter tld", "user@example.c", false},
		{"numeric tld", "user@example.12", false},
		{"leading hyphen label", "user@-example.com", false},
		{"trailing hyphen label", "user@example-.com", false},
		{"bracketed ip", "user@[192.168.0.1]", false},
		{"underscore in domain", "user@exa_mple.com", false},

		// --- header syntax that net/mail alone would accept ---
		{"display name", "Ai Do <user@example.com>", false},
		{"angle brackets", "<user@example.com>", false},
		{"quoted local", `"odd name"@example.com`, false},

		// --- length limits ---
		{"local at limit", strings.Repeat("a", 64) + "@example.com", true},
		{"local over limit", strings.Repeat("a", 65) + "@example.com", false},
		{"label over limit", "user@" + strings.Repeat("a", 64) + ".com", false},
		{
			"address over limit",
			strings.Repeat("a", 64) + "@" + strings.Repeat("b", 60) + "." +
				strings.Repeat("c", 60) + "." + strings.Repeat("d", 60) + "." +
				strings.Repeat("e", 60) + ".com",
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidEmail(tt.email); got != tt.want {
				t.Errorf("isValidEmail(%q) = %v, want %v", tt.email, got, tt.want)
			}
		})
	}
}

// Both handlers normalize before validating, so an address that only differs
// by case or padding must survive the round trip.
func TestIsValidEmailAfterNormalize(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"  User@Example.COM ", "USER@EXAMPLE.COM", "user@example.com"} {
		if !isValidEmail(normalizeEmail(raw)) {
			t.Errorf("isValidEmail(normalizeEmail(%q)) = false, want true", raw)
		}
	}
}

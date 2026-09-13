package validators

import "testing"

func TestIsValidAddress(t *testing.T) {
	cases := map[string]bool{
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed": true,  // correct EIP-55 checksum
		"0x5aaeb6053f3e94c9b9a09f33669435e7ef1beaed": true,  // all-lowercase, no checksum asserted
		"0x5AAEB6053F3E94C9B9A09F33669435E7EF1BEAED": true,  // all-uppercase, no checksum asserted
		"0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAEd": false, // wrong checksum (last two chars flipped case)
		"5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed":   false, // missing 0x prefix
		"0x1234":         false, // too short
		"not an address": false,
	}
	for addr, want := range cases {
		if got := IsValidAddress(addr); got != want {
			t.Errorf("IsValidAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestIsValidEmailUsernameTokenSymbol(t *testing.T) {
	if !IsValidEmail("user@example.com") {
		t.Error("expected a valid email to pass")
	}
	if IsValidEmail("not-an-email") {
		t.Error("expected an invalid email to fail")
	}
	if !IsValidUsername("alice_123") {
		t.Error("expected a valid username to pass")
	}
	if IsValidUsername("ab") {
		t.Error("expected a too-short username to fail")
	}
	if !IsValidTokenSymbol("USDC") {
		t.Error("expected a valid token symbol to pass")
	}
	if IsValidTokenSymbol("") {
		t.Error("expected an empty token symbol to fail")
	}
}

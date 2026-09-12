// Package validators holds simple, dependency-light format checks shared by
// every component. Business-rule validation (e.g. "is this user allowed to
// do X") belongs in the component's services package, not here.
package validators

import (
	"regexp"

	"github.com/stellar/go-stellar-sdk/keypair"
)

var (
	emailRegex     = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	usernameRegex  = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	assetCodeRegex = regexp.MustCompile(`^[a-zA-Z0-9]{1,12}$`)
)

// IsValidEmail reports whether s looks like an email address.
func IsValidEmail(s string) bool { return emailRegex.MatchString(s) }

// IsValidUsername reports whether s is a valid handle (3-32 alphanumeric/underscore chars).
func IsValidUsername(s string) bool { return usernameRegex.MatchString(s) }

// IsValidAssetCode reports whether s is a valid Stellar asset code (1-12 alphanumeric chars).
func IsValidAssetCode(s string) bool { return assetCodeRegex.MatchString(s) }

// IsValidStellarPublicKey reports whether s is a syntactically valid Stellar
// ed25519 public address (a "G..." strkey).
func IsValidStellarPublicKey(s string) bool {
	_, err := keypair.ParseAddress(s)
	return err == nil
}

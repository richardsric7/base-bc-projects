// Package validators holds simple, dependency-light format checks shared by
// every component. Business-rule validation (e.g. "is this user allowed to
// do X") belongs in the component's services package, not here.
package validators

import (
	"regexp"
	"strings"

	"github.com/ethereum/go-ethereum/common"
)

var (
	emailRegex       = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	usernameRegex    = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	tokenSymbolRegex = regexp.MustCompile(`^[a-zA-Z0-9]{1,12}$`)
)

// IsValidEmail reports whether s looks like an email address.
func IsValidEmail(s string) bool { return emailRegex.MatchString(s) }

// IsValidUsername reports whether s is a valid handle (3-32 alphanumeric/underscore chars).
func IsValidUsername(s string) bool { return usernameRegex.MatchString(s) }

// IsValidTokenSymbol reports whether s is a plausible ERC-20 token symbol
// (1-12 alphanumeric characters) - a display-catalog check, not an
// on-chain one; the actual symbol lives in the token contract.
func IsValidTokenSymbol(s string) bool { return tokenSymbolRegex.MatchString(s) }

// IsValidAddress reports whether s is a syntactically valid EVM address:
// "0x" followed by 40 hex characters, and - if it uses mixed case at all -
// a correct EIP-55 checksum. An all-lowercase or all-uppercase address is
// accepted with no checksum asserted, per EIP-55; a mixed-case address
// whose checksum doesn't match is rejected rather than silently accepted,
// since that's the case EIP-55 exists to catch (a typo'd address that
// otherwise looks plausible).
func IsValidAddress(s string) bool {
	if !strings.HasPrefix(s, "0x") || !common.IsHexAddress(s) {
		return false
	}
	hexPart := strings.TrimPrefix(s, "0x")
	if hexPart == strings.ToLower(hexPart) || hexPart == strings.ToUpper(hexPart) {
		return true
	}
	return common.HexToAddress(s).Hex() == s
}

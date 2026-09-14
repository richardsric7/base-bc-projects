package middleware

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"wallet-backend/internal/apperrors"
	sharedaccessModels "wallet-backend/internal/components/sharedaccess/models"
	usersModels "wallet-backend/internal/components/users/models"
	"wallet-backend/internal/cryptoutil"
	"wallet-backend/internal/validators"
)

// CtxSigner is the gin context key SignatureAuth stores the verified
// signer's address under - the address that actually produced the
// signature, as opposed to CtxSubject, which is the wallet address the
// request acts on (itself, or a wallet it's delegated to act on via
// sharedaccess). Most handlers should keep reading CtxSubject exactly as
// they do today; CtxSigner is for the few call sites that specifically
// need to know who signed - e.g. attributing a sharedaccess proposal or
// approval to its actual author when it differs from the wallet acted on.
const CtxSigner = "signer"

const (
	headerSignerAddress = "X-Signer-Address"
	headerWalletAddress = "X-Wallet-Address"
	headerSignature     = "X-Signature"
	headerTimestamp     = "X-Timestamp"
)

// SignatureAuth is the per-request signature-verification middleware that
// replaces SIWE + session JWT for user operations (PLAN.md §12): every
// request signs fullPathWithQuery+signerAddress+timestamp with the
// signer's own key (personal_sign/EIP-191), verified fully offline via
// cryptoutil.VerifyPersonalSign - no server-side session or nonce state,
// matching PLAN.md §12.1's original per-request scheme with one addition
// this design makes deliberately: toleranceSeconds bounds how far
// X-Timestamp may drift from the server's clock in either direction
// (PLAN.md §12.3), which is the entire defense against replaying a
// captured, valid signature indefinitely - the confirmed gap in the
// original that this design closes rather than reproduces.
//
// Since PLAN.md §13.2, a wallet's own address is a Safe smart-contract
// account, never the signer's own EOA - so the ordinary case now always
// has signer != wallet, and is authorized by the signer being that
// wallet's owning User.SignerAddress (see signerIsPrimaryWalletOwner)
// rather than by the equal-address fast path below, which mainly exists
// today for account-recovery Branch A's bare, undeployed identities
// (PLAN.md §15) where signer and wallet are deliberately the same
// address. A request naming a wallet the signer neither equals nor owns
// is authorized only if the signer holds any sharedaccess GroupMember
// role on that wallet (PLAN.md §12.4) - the finer distinction between
// view/initiate/approve stays a handler-level concern, exactly as in the
// original, rather than being duplicated here.
func SignatureAuth(db *gorm.DB, toleranceSeconds int) gin.HandlerFunc {
	return func(c *gin.Context) {
		signer := c.GetHeader(headerSignerAddress)
		wallet := c.GetHeader(headerWalletAddress)
		signatureHex := c.GetHeader(headerSignature)
		timestampStr := c.GetHeader(headerTimestamp)

		if signer == "" || wallet == "" || signatureHex == "" || timestampStr == "" {
			apperrors.Abort(c, apperrors.Unauthorized("missing signature headers"))
			return
		}
		if !validators.IsValidAddress(signer) {
			apperrors.Abort(c, apperrors.Unauthorized("invalid "+headerSignerAddress))
			return
		}
		if !validators.IsValidAddress(wallet) {
			apperrors.Abort(c, apperrors.Unauthorized("invalid "+headerWalletAddress))
			return
		}
		if !strings.HasPrefix(signatureHex, "0x") {
			apperrors.Abort(c, apperrors.Unauthorized(headerSignature+" must be 0x-prefixed"))
			return
		}

		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			apperrors.Abort(c, apperrors.Unauthorized("invalid "+headerTimestamp))
			return
		}
		drift := time.Now().Unix() - timestamp
		if drift < 0 {
			drift = -drift
		}
		if drift > int64(toleranceSeconds) {
			apperrors.Abort(c, apperrors.Unauthorized("request timestamp is outside the allowed tolerance window"))
			return
		}

		message := c.Request.URL.RequestURI() + signer + timestampStr
		valid, err := cryptoutil.VerifyPersonalSign(message, common.FromHex(signatureHex), common.HexToAddress(signer))
		if err != nil || !valid {
			apperrors.Abort(c, apperrors.Unauthorized("invalid signature"))
			return
		}

		signerAddr := common.HexToAddress(signer).Hex()
		walletAddr := common.HexToAddress(wallet).Hex()

		if !strings.EqualFold(signerAddr, walletAddr) {
			isOwner, err := signerIsPrimaryWalletOwner(db, walletAddr, signerAddr)
			if err != nil {
				apperrors.AbortAny(c, err)
				return
			}
			if !isOwner {
				authorized, err := signerHasStandingOn(db, walletAddr, signerAddr)
				if err != nil {
					apperrors.AbortAny(c, err)
					return
				}
				if !authorized {
					apperrors.Abort(c, apperrors.Forbidden("signer is not authorized to act on this wallet"))
					return
				}
			}
		}

		c.Set(CtxSubject, walletAddr)
		c.Set(CtxSigner, signerAddr)
		c.Next()
	}
}

// signerIsPrimaryWalletOwner reports whether signerAddr is the
// User.SignerAddress currently authorized to operate the primary wallet
// at walletAddr (PLAN.md §13.2) - the ordinary self-service case now that
// a wallet's own address is a Safe rather than the signer's own EOA. A
// lookup miss here (walletAddr isn't any user's primary wallet at all -
// it's a sub-wallet or shared-access group instead) is not an error: the
// caller falls through to signerHasStandingOn next.
func signerIsPrimaryWalletOwner(db *gorm.DB, walletAddr, signerAddr string) (bool, error) {
	var count int64
	err := db.Model(&usersModels.User{}).
		Where("LOWER(address) = LOWER(?) AND LOWER(signer_address) = LOWER(?)", walletAddr, signerAddr).
		Count(&count).Error
	if err != nil {
		return false, apperrors.Internal("failed to resolve wallet owner")
	}
	return count > 0, nil
}

// signerHasStandingOn reports whether signerAddr holds any sharedaccess
// GroupMember role on the group whose Address is walletAddr. Comparisons
// are case-insensitive since neither ClosedGroup.Address nor
// GroupMember.MemberAddress is guaranteed to be stored in a single
// canonical case today - this middleware only decides whether the signer
// may proceed at all, never which role they hold, so a stray fixed-case
// lookup miss here would incorrectly lock out an otherwise-valid member.
func signerHasStandingOn(db *gorm.DB, walletAddr, signerAddr string) (bool, error) {
	var group sharedaccessModels.ClosedGroup
	err := db.Where("LOWER(address) = LOWER(?)", walletAddr).First(&group).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, apperrors.Internal("failed to resolve wallet")
	}

	var count int64
	err = db.Model(&sharedaccessModels.GroupMember{}).
		Where("group_id = ? AND LOWER(member_address) = LOWER(?)", group.ID, signerAddr).
		Count(&count).Error
	if err != nil {
		return false, apperrors.Internal("failed to resolve wallet access")
	}
	return count > 0, nil
}

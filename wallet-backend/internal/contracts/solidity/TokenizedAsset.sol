// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

/// @title TokenizedAsset
/// @notice A mintable, burnable, ownable, transfer-restricted ERC-20 - the
/// Base equivalent of a Trovo-issued Stellar asset (see PLAN.md §4.9 and
/// §5, and §22's restricted-asset correction). Every tokenized asset is a
/// regulated/restricted security: on Stellar this was enforced via the
/// issuer account's AUTH_REQUIRED flag plus a per-holder trustline the
/// issuer must explicitly authorize before that address may hold, send,
/// or receive the asset at all - a subscription's own transaction bundled
/// the trustline-authorize operation inline (`generateAssetSubscriptionXdr`),
/// so a buyer's wallet became authorized as an atomic part of purchasing.
/// This contract reproduces exactly that policy with an on-chain
/// allow-list (`isAuthorized`) rather than a Stellar-specific trustline:
/// only the contract owner (the backend's per-asset issuer key) may
/// authorize/deauthorize an address, every transfer (mint included)
/// requires both sides already authorized, and `mint` auto-authorizes its
/// recipient - the same "the issuer funding you IS the authorization"
/// bundling Stellar's own subscription flow had. Ordinary transfers
/// (Sale.buy() payouts, secondary-market trades) require the recipient to
/// already be authorized separately - see
/// tokenization/services.authorizeHolder, called by the backend before
/// building a purchase, gated on that buyer's own KYC status exactly as
/// the original's SubscribeToTokenizedAsset gated on
/// `walletOwner.KYCVerified`.
/// @dev A fixed custom `decimals` (rather than always 18) mirrors the
/// original's ability to declare an asset's own decimal precision.
contract TokenizedAsset is ERC20Burnable, Ownable {
    uint8 private immutable _customDecimals;

    /// @notice Whether `account` may hold, send, or receive this asset.
    /// Never true for a fresh address until the issuer (owner) explicitly
    /// authorizes it (or mints to it, which authorizes as a side effect).
    mapping(address => bool) public isAuthorized;

    event Authorized(address indexed account);
    event Deauthorized(address indexed account);

    constructor(
        string memory name_,
        string memory symbol_,
        uint8 decimals_,
        address initialOwner
    ) ERC20(name_, symbol_) {
        _customDecimals = decimals_;
        _transferOwnership(initialOwner);
    }

    function decimals() public view virtual override returns (uint8) {
        return _customDecimals;
    }

    /// @notice Authorizes `account` to hold/send/receive this asset -
    /// called by the backend's per-asset issuer key once it has verified
    /// whatever off-chain compliance/KYC gate applies (see
    /// tokenization/services.authorizeHolder), mirroring the original's
    /// issuer-authorized-trustline step.
    function authorize(address account) external onlyOwner {
        isAuthorized[account] = true;
        emit Authorized(account);
    }

    /// @notice Revokes `account`'s authorization - a compliance action
    /// (e.g. a failed re-verification or a regulatory freeze), the
    /// equivalent of the issuer revoking a Stellar trustline's
    /// authorization via SetTrustLineFlags. A revoked holder keeps their
    /// existing balance (this contract has no seize/clawback) but can no
    /// longer send or receive further amounts.
    function deauthorize(address account) external onlyOwner {
        isAuthorized[account] = false;
        emit Deauthorized(account);
    }

    /// @notice Mints `amount` of the asset to `to` - called by the
    /// backend's minting-authority key (cryptoutil.DeriveKey per PLAN.md
    /// §2) once an off-chain approval/compliance check has passed, e.g.
    /// a completed fiat purchase or a matched primary-sale allocation.
    /// Auto-authorizes `to` if it isn't already - see this contract's own
    /// doc comment for why minting and authorization are bundled here
    /// exactly as they were in the original's own subscription flow.
    function mint(address to, uint256 amount) external onlyOwner {
        if (!isAuthorized[to]) {
            isAuthorized[to] = true;
            emit Authorized(to);
        }
        _mint(to, amount);
    }

    /// @dev Enforces the allow-list on every balance-changing transfer.
    /// `from == address(0)` (minting) and `to == address(0)` (burning)
    /// are exempt on that side - minting is handled by `mint` above
    /// (which authorizes first), and burning only ever removes from an
    /// already-authorized holder's own balance.
    function _beforeTokenTransfer(address from, address to, uint256 amount) internal virtual override {
        super._beforeTokenTransfer(from, to, amount);
        if (from != address(0)) {
            require(isAuthorized[from], "TokenizedAsset: sender not authorized");
        }
        if (to != address(0)) {
            require(isAuthorized[to], "TokenizedAsset: recipient not authorized");
        }
    }
}

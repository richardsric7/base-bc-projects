// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/ERC20Burnable.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

/// @title TokenizedAsset
/// @notice A minimal mintable, burnable, ownable ERC-20 - the Base
/// equivalent of a Trovo-issued Stellar asset (see PLAN.md §4.9 and §5).
/// Deliberately plain rather than a compliance-aware standard like
/// ERC-1400: the original's actual on-chain representation of a
/// tokenized asset was just a Stellar asset code/issuer pair with no
/// on-chain transfer restrictions either, with every compliance/KYC gate
/// enforced off-chain by the backend before it would ever build a
/// transfer for a restricted asset. This contract keeps that same split:
/// the chain enforces supply and ownership, the backend enforces who is
/// allowed to end up holding it.
/// @dev A fixed custom `decimals` (rather than always 18) mirrors the
/// original's ability to declare an asset's own decimal precision.
contract TokenizedAsset is ERC20Burnable, Ownable {
    uint8 private immutable _customDecimals;

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

    /// @notice Mints `amount` of the asset to `to` - called by the
    /// backend's minting-authority key (cryptoutil.DeriveKey per PLAN.md
    /// §2) once an off-chain approval/compliance check has passed, e.g.
    /// a completed fiat purchase or a matched primary-sale allocation.
    function mint(address to, uint256 amount) external onlyOwner {
        _mint(to, amount);
    }
}

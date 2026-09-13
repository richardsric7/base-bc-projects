// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/extensions/IERC20Metadata.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

/// @title Sale
/// @notice A minimal atomic primary-sale contract for a tokenized asset
/// (PLAN.md §4.9/§5) - a buyer's approve() of the payment token followed
/// by one buy() call is the entire purchase flow, both legs (payment in,
/// asset out) settling in the same transaction rather than the two
/// independent transfers the market component's off-chain order book
/// needs (see PLAN.md §4.8's documented atomicity limitation - this
/// contract is exactly the fix that gap describes, for the one flow that
/// justified building a contract for it now).
/// @dev The sale contract holds its own balance of `asset` (pre-funded by
/// the backend's minting-authority key minting the sale's allocation to
/// this contract's address before the sale opens) and simply transfers
/// out of that balance on each purchase, rather than minting directly -
/// keeping this contract from ever needing minting rights itself.
contract Sale is Ownable {
    IERC20 public immutable asset;
    IERC20 public immutable paymentToken;
    /// @notice Price of one whole unit of `asset` (i.e. 10**asset.decimals()
    /// base units), denominated in paymentToken base units.
    uint256 public immutable pricePerUnit;
    address public proceedsRecipient;
    uint256 public totalSold;
    bool public paused;

    event Purchased(address indexed buyer, uint256 assetAmount, uint256 paymentAmount);
    event Paused(bool paused);

    constructor(
        address asset_,
        address paymentToken_,
        uint256 pricePerUnit_,
        address proceedsRecipient_,
        address initialOwner
    ) {
        asset = IERC20(asset_);
        paymentToken = IERC20(paymentToken_);
        pricePerUnit = pricePerUnit_;
        proceedsRecipient = proceedsRecipient_;
        _transferOwnership(initialOwner);
    }

    /// @notice Buys `assetAmount` base units of the asset, paying
    /// `assetAmount * pricePerUnit / 10**asset.decimals()` of paymentToken -
    /// the caller must have approved this contract for at least that much
    /// beforehand.
    function buy(uint256 assetAmount) external {
        require(!paused, "sale is paused");
        require(assetAmount > 0, "amount must be positive");

        uint8 assetDecimals = IERC20Metadata(address(asset)).decimals();
        uint256 paymentAmount = (assetAmount * pricePerUnit) / (10 ** assetDecimals);
        require(paymentAmount > 0, "amount too small");

        require(paymentToken.transferFrom(msg.sender, proceedsRecipient, paymentAmount), "payment transfer failed");
        require(asset.transfer(msg.sender, assetAmount), "asset transfer failed - insufficient sale inventory");

        totalSold += assetAmount;
        emit Purchased(msg.sender, assetAmount, paymentAmount);
    }

    /// @notice Lets the owner pause/resume new purchases (e.g. once a
    /// sale's allocation or time window has ended) without needing to
    /// deploy a new contract.
    function setPaused(bool paused_) external onlyOwner {
        paused = paused_;
        emit Paused(paused_);
    }

    /// @notice Reclaims unsold asset inventory, e.g. once a sale has
    /// ended, back to the issuer.
    function withdrawUnsold(address to, uint256 amount) external onlyOwner {
        require(asset.transfer(to, amount), "withdraw failed");
    }
}

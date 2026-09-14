// SPDX-License-Identifier: MIT
pragma solidity ^0.8.24;

/// @dev Minimal ERC-165, declared locally rather than pulled from
/// OpenZeppelin - the only external-facing requirement here is that
/// `supportsInterface` exists with the standard selector, not a whole
/// dependency for one four-line interface.
interface IERC165 {
    function supportsInterface(bytes4 interfaceId) external view returns (bool);
}

/// @dev Safe's own Guard interface (`base/GuardManager.sol` in
/// safe-global/safe-contracts, v1.4.1 - the same version this port's Go
/// side hardcodes in internal/safe/safe.go). `operation` is declared as
/// `uint8` rather than importing Safe's `Enum.Operation` type: Solidity's
/// ABI canonicalizes an enum parameter to its underlying `uint8` in the
/// function selector either way, so this produces the exact same
/// selectors - and therefore the exact same `type(Guard).interfaceId` -
/// as Safe's real interface, without vendoring Safe's own Enum library
/// for one parameter.
interface Guard is IERC165 {
    function checkTransaction(
        address to,
        uint256 value,
        bytes memory data,
        uint8 operation,
        uint256 safeTxGas,
        uint256 baseGas,
        uint256 gasPrice,
        address gasToken,
        address payable refundReceiver,
        bytes memory signatures,
        address msgSender
    ) external;

    function checkAfterExecution(bytes32 txHash, bool success) external;
}

/// @title RecoveryGuard
/// @notice PLAN.md §15.3's recommended above-parity hardening for Branch B
/// wallet recovery: a Safe Guard that restricts what a transaction may do
/// whenever it was co-signed by the recovery service's own owner slot,
/// narrowing that owner's real-world blast radius from "can authorize
/// anything the user could" (Safe has no native per-owner permission
/// system) down to "can only ever rotate the Safe's own owners" -
/// specifically `swapOwner`, `addOwnerWithThreshold`, `removeOwner` and
/// `changeThreshold`, and only as a self-call (`to == address(this)`,
/// i.e. the Safe managing its own owner list) with no ETH value attached.
/// A transaction the recovery service did NOT co-sign is never touched by
/// this guard at all - the wallet's own owner can still spend normally.
///
/// One RecoveryGuard instance is deployed once as shared platform
/// infrastructure (see internal/components/users/services/
/// recovery_platform.go) and installed via `Safe.setGuard` on every
/// individual primary wallet that enables Branch B - it holds no
/// per-Safe state, only the one immutable recoveryServiceOwner address
/// every enrolled Safe's second owner slot is expected to be.
contract RecoveryGuard is Guard {
    /// @notice The recovery service's own address - always a Safe under
    /// this design (PLAN.md §15.3's recommendation that it hold genuine
    /// internal N-of-M among the platform's trusted operators, never a
    /// single hot EOA), which is exactly what makes _signedByRecoveryService
    /// below correct: a Safe's contribution to another Safe's combined
    /// signature blob is always the EIP-1271 "contract signature" form
    /// (v == 0, r == the contract's own address - see
    /// internal/safe/signatures.go's PackSignatures), never a raw ECDSA
    /// signature, so recognizing it needs no ecrecover at all, just a
    /// direct address comparison against each slot's r value.
    address public immutable recoveryServiceOwner;

    constructor(address _recoveryServiceOwner) {
        require(_recoveryServiceOwner != address(0), "RecoveryGuard: recoveryServiceOwner is required");
        recoveryServiceOwner = _recoveryServiceOwner;
    }

    /// @inheritdoc Guard
    /// @dev Called by the Safe itself (so `msg.sender` here is the Safe
    /// under guard, not the transaction's external submitter - `to ==
    /// msg.sender` therefore correctly tests "this call targets the
    /// Safe's own address", the same self-call shape every owner-
    /// management call in this codebase already uses, see
    /// internal/safe/safe.go's EncodeAddOwnerWithThresholdCalldata doc
    /// comment) immediately before execution, and reverts to block it.
    function checkTransaction(
        address to,
        uint256 value,
        bytes memory data,
        uint8 /* operation */,
        uint256 /* safeTxGas */,
        uint256 /* baseGas */,
        uint256 /* gasPrice */,
        address /* gasToken */,
        address payable /* refundReceiver */,
        bytes memory signatures,
        address /* msgSender */
    ) external view override {
        if (!_signedByRecoveryService(signatures)) {
            return;
        }
        require(to == msg.sender, "RecoveryGuard: recovery service may only self-call for owner management");
        require(value == 0, "RecoveryGuard: recovery service may not authorize a value transfer");
        require(_isOwnerManagementCall(data), "RecoveryGuard: recovery service may only call owner-management functions");
    }

    /// @inheritdoc Guard
    /// @dev Nothing to check after the fact - every restriction this
    /// guard enforces is about what the call was allowed to do, decided
    /// entirely from checkTransaction's own arguments.
    function checkAfterExecution(bytes32 /* txHash */, bool /* success */) external pure override {}

    /// @inheritdoc IERC165
    function supportsInterface(bytes4 interfaceId) external pure override returns (bool) {
        return interfaceId == type(Guard).interfaceId || interfaceId == type(IERC165).interfaceId;
    }

    /// @dev Scans Safe's packed signatures blob (see PackSignatures) for a
    /// contract-signature slot (v == 0) whose r equals
    /// recoveryServiceOwner - i.e. "did the recovery service's Safe
    /// co-sign this transaction", not merely "is it currently an owner".
    /// That distinction matters: if this checked ownership alone, it
    /// would also restrict the wallet's own legitimate owner acting
    /// without the recovery service at all, defeating the point of
    /// enrolling in the first place.
    function _signedByRecoveryService(bytes memory signatures) private view returns (bool) {
        uint256 count = signatures.length / 65;
        for (uint256 i = 0; i < count; i++) {
            (uint8 v, bytes32 r, ) = _signatureSplit(signatures, i);
            if (v == 0 && address(uint160(uint256(r))) == recoveryServiceOwner) {
                return true;
            }
        }
        return false;
    }

    /// @dev Reproduces Safe's own SignatureDecoder.signatureSplit: each
    /// packed signature occupies a fixed 65-byte slot (r: bytes 0-31, s:
    /// bytes 32-63, v: byte 64), regardless of which of Safe's three
    /// signature kinds it holds - see internal/safe/signatures.go's
    /// PackSignatures for the Go side building this same layout.
    function _signatureSplit(bytes memory signatures, uint256 pos) private pure returns (uint8 v, bytes32 r, bytes32 s) {
        assembly {
            let signaturePos := mul(0x41, pos)
            r := mload(add(signatures, add(signaturePos, 0x20)))
            s := mload(add(signatures, add(signaturePos, 0x40)))
            v := byte(0, mload(add(signatures, add(signaturePos, 0x60))))
        }
    }

    /// @dev True only for the four Safe OwnerManager selectors this
    /// guard permits when the recovery service co-signs: swapOwner (the
    /// one PLAN.md §15.5 step 2 actually performs during a real
    /// recovery), plus addOwnerWithThreshold/removeOwner/changeThreshold
    /// (the enable/disable-enrollment self-calls in PLAN.md §15.9 Phase
    /// 3) so a Safe already relying on the recovery service to manage
    /// its own enrollment isn't blocked from doing so by this same guard.
    function _isOwnerManagementCall(bytes memory data) private pure returns (bool) {
        if (data.length < 4) {
            return false;
        }
        bytes4 selector = _selector(data);
        return
            selector == bytes4(keccak256("swapOwner(address,address,address)")) ||
            selector == bytes4(keccak256("addOwnerWithThreshold(address,uint256)")) ||
            selector == bytes4(keccak256("removeOwner(address,address,uint256)")) ||
            selector == bytes4(keccak256("changeThreshold(uint256)"));
    }

    function _selector(bytes memory data) private pure returns (bytes4 selector) {
        assembly {
            selector := mload(add(data, 0x20))
        }
    }
}

// Package contracts embeds the two Solidity contracts PLAN.md §5 calls
// for (compiled ahead of time, never at runtime - see solidity/README.md
// for the toolchain and how to reproduce the build) and provides Go
// helpers to deploy them and encode calls against them, reusing
// internal/network's existing build/sign/submit machinery rather than
// introducing go-ethereum's separate accounts/abi/bind package.
package contracts

import (
	_ "embed"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

//go:embed artifacts/TokenizedAsset.abi.json
var tokenizedAssetABIJSON string

//go:embed artifacts/TokenizedAsset.bin
var tokenizedAssetBytecodeHex string

//go:embed artifacts/Sale.abi.json
var saleABIJSON string

//go:embed artifacts/Sale.bin
var saleBytecodeHex string

// TokenizedAssetABI and SaleABI are parsed once at package init - both
// embedded JSON strings are produced by this package's own build (see
// solidity/README.md), so a parse failure here means the checked-in
// artifact is corrupt, not something a caller can recover from.
var TokenizedAssetABI abi.ABI
var SaleABI abi.ABI

func init() {
	var err error
	TokenizedAssetABI, err = abi.JSON(strings.NewReader(tokenizedAssetABIJSON))
	if err != nil {
		panic("contracts: invalid embedded TokenizedAsset ABI: " + err.Error())
	}
	SaleABI, err = abi.JSON(strings.NewReader(saleABIJSON))
	if err != nil {
		panic("contracts: invalid embedded Sale ABI: " + err.Error())
	}
}

func tokenizedAssetBytecode() []byte { return common.FromHex(tokenizedAssetBytecodeHex) }
func saleBytecode() []byte           { return common.FromHex(saleBytecodeHex) }

// TokenizedAssetDeployData ABI-encodes TokenizedAsset's constructor
// (name, symbol, decimals, initialOwner) appended to its bytecode -
// exactly what network.Client.DeployContract needs as its data argument.
// initialOwner is the address that will hold minting rights - typically
// a cryptoutil.DeriveKey-derived minting-authority key, per PLAN.md §2.
func TokenizedAssetDeployData(name, symbol string, decimals uint8, initialOwner string) ([]byte, error) {
	args, err := TokenizedAssetABI.Pack("", name, symbol, decimals, common.HexToAddress(initialOwner))
	if err != nil {
		return nil, fmt.Errorf("encode TokenizedAsset constructor: %w", err)
	}
	return append(tokenizedAssetBytecode(), args...), nil
}

// EncodeMint ABI-encodes a TokenizedAsset.mint(to, amount) call - callable
// only by whatever address the contract was deployed with as
// initialOwner.
func EncodeMint(to string, amount *big.Int) ([]byte, error) {
	data, err := TokenizedAssetABI.Pack("mint", common.HexToAddress(to), amount)
	if err != nil {
		return nil, fmt.Errorf("encode mint: %w", err)
	}
	return data, nil
}

// EncodeBurn ABI-encodes a TokenizedAsset.burn(amount) call - burns from
// the caller's own balance (ERC20Burnable, no special permission needed).
func EncodeBurn(amount *big.Int) ([]byte, error) {
	data, err := TokenizedAssetABI.Pack("burn", amount)
	if err != nil {
		return nil, fmt.Errorf("encode burn: %w", err)
	}
	return data, nil
}

// SaleDeployData ABI-encodes Sale's constructor (asset, paymentToken,
// pricePerUnit, proceedsRecipient, initialOwner) appended to its
// bytecode. pricePerUnit is payment-token base units per whole unit
// (10**asset.decimals()) of the asset - see Sale.sol's doc comment.
func SaleDeployData(assetAddress, paymentTokenAddress string, pricePerUnit *big.Int, proceedsRecipient, initialOwner string) ([]byte, error) {
	args, err := SaleABI.Pack("", common.HexToAddress(assetAddress), common.HexToAddress(paymentTokenAddress), pricePerUnit, common.HexToAddress(proceedsRecipient), common.HexToAddress(initialOwner))
	if err != nil {
		return nil, fmt.Errorf("encode Sale constructor: %w", err)
	}
	return append(saleBytecode(), args...), nil
}

// EncodeBuy ABI-encodes a Sale.buy(assetAmount) call.
func EncodeBuy(assetAmount *big.Int) ([]byte, error) {
	data, err := SaleABI.Pack("buy", assetAmount)
	if err != nil {
		return nil, fmt.Errorf("encode buy: %w", err)
	}
	return data, nil
}

// EncodeSetPaused ABI-encodes a Sale.setPaused(paused) call - callable
// only by the Sale contract's owner.
func EncodeSetPaused(paused bool) ([]byte, error) {
	data, err := SaleABI.Pack("setPaused", paused)
	if err != nil {
		return nil, fmt.Errorf("encode setPaused: %w", err)
	}
	return data, nil
}

// EncodeWithdrawUnsold ABI-encodes a Sale.withdrawUnsold(to, amount) call -
// callable only by the Sale contract's owner.
func EncodeWithdrawUnsold(to string, amount *big.Int) ([]byte, error) {
	data, err := SaleABI.Pack("withdrawUnsold", common.HexToAddress(to), amount)
	if err != nil {
		return nil, fmt.Errorf("encode withdrawUnsold: %w", err)
	}
	return data, nil
}

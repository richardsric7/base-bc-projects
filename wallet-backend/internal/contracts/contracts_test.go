package contracts

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// These tests verify the ABI-encoding logic (constructor args, method
// calls) by decoding what was packed and checking it round-trips - the
// same posture this port takes for every vendor integration where no live
// endpoint is available in this sandbox: encoding/decoding is verified
// directly rather than against a live chain. Deploying and calling these
// contracts end-to-end needs a real (or simulated) EVM, which is outside
// this package's test scope - see PLAN.md §5.

func TestTokenizedAssetDeployData(t *testing.T) {
	owner := "0x000000000000000000000000000000000000aa"
	data, err := TokenizedAssetDeployData("Trovo Gold", "TGLD", 7, owner)
	if err != nil {
		t.Fatalf("TokenizedAssetDeployData returned error: %v", err)
	}

	bytecode := tokenizedAssetBytecode()
	if !bytes.HasPrefix(data, bytecode) {
		t.Fatal("expected the deploy data to start with the contract's bytecode")
	}
	argsData := data[len(bytecode):]

	unpacked, err := TokenizedAssetABI.Constructor.Inputs.Unpack(argsData)
	if err != nil {
		t.Fatalf("failed to unpack constructor args: %v", err)
	}
	if unpacked[0].(string) != "Trovo Gold" || unpacked[1].(string) != "TGLD" {
		t.Fatalf("unexpected name/symbol: %+v", unpacked)
	}
	if unpacked[2].(uint8) != 7 {
		t.Fatalf("unexpected decimals: %+v", unpacked[2])
	}
	if unpacked[3].(common.Address) != common.HexToAddress(owner) {
		t.Fatalf("unexpected owner: %+v", unpacked[3])
	}
}

func TestEncodeMint(t *testing.T) {
	to := "0x000000000000000000000000000000000000bb"
	amount := big.NewInt(1_000_000)
	data, err := EncodeMint(to, amount)
	if err != nil {
		t.Fatalf("EncodeMint returned error: %v", err)
	}

	method, err := TokenizedAssetABI.MethodById(data[:4])
	if err != nil {
		t.Fatalf("failed to resolve method by selector: %v", err)
	}
	if method.Name != "mint" {
		t.Fatalf("expected method 'mint', got %q", method.Name)
	}
	unpacked, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack mint args: %v", err)
	}
	if unpacked[0].(common.Address) != common.HexToAddress(to) {
		t.Fatalf("unexpected 'to': %+v", unpacked[0])
	}
	if unpacked[1].(*big.Int).Cmp(amount) != 0 {
		t.Fatalf("unexpected amount: %+v", unpacked[1])
	}
}

func TestEncodeBurn(t *testing.T) {
	amount := big.NewInt(500)
	data, err := EncodeBurn(amount)
	if err != nil {
		t.Fatalf("EncodeBurn returned error: %v", err)
	}
	method, err := TokenizedAssetABI.MethodById(data[:4])
	if err != nil {
		t.Fatalf("failed to resolve method by selector: %v", err)
	}
	if method.Name != "burn" {
		t.Fatalf("expected method 'burn', got %q", method.Name)
	}
}

func TestSaleDeployData(t *testing.T) {
	asset := "0x000000000000000000000000000000000000cc"
	paymentToken := "0x000000000000000000000000000000000000dd"
	proceeds := "0x000000000000000000000000000000000000ee"
	owner := "0x000000000000000000000000000000000000ff"
	price := big.NewInt(2_500_000)

	data, err := SaleDeployData(asset, paymentToken, price, proceeds, owner)
	if err != nil {
		t.Fatalf("SaleDeployData returned error: %v", err)
	}

	bytecode := saleBytecode()
	if !bytes.HasPrefix(data, bytecode) {
		t.Fatal("expected the deploy data to start with the contract's bytecode")
	}
	argsData := data[len(bytecode):]

	unpacked, err := SaleABI.Constructor.Inputs.Unpack(argsData)
	if err != nil {
		t.Fatalf("failed to unpack constructor args: %v", err)
	}
	if unpacked[0].(common.Address) != common.HexToAddress(asset) {
		t.Fatalf("unexpected asset address: %+v", unpacked[0])
	}
	if unpacked[1].(common.Address) != common.HexToAddress(paymentToken) {
		t.Fatalf("unexpected payment token address: %+v", unpacked[1])
	}
	if unpacked[2].(*big.Int).Cmp(price) != 0 {
		t.Fatalf("unexpected price: %+v", unpacked[2])
	}
	if unpacked[3].(common.Address) != common.HexToAddress(proceeds) {
		t.Fatalf("unexpected proceeds recipient: %+v", unpacked[3])
	}
	if unpacked[4].(common.Address) != common.HexToAddress(owner) {
		t.Fatalf("unexpected owner: %+v", unpacked[4])
	}
}

func TestEncodeBuy(t *testing.T) {
	amount := big.NewInt(42)
	data, err := EncodeBuy(amount)
	if err != nil {
		t.Fatalf("EncodeBuy returned error: %v", err)
	}
	method, err := SaleABI.MethodById(data[:4])
	if err != nil {
		t.Fatalf("failed to resolve method by selector: %v", err)
	}
	if method.Name != "buy" {
		t.Fatalf("expected method 'buy', got %q", method.Name)
	}
	unpacked, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack buy args: %v", err)
	}
	if unpacked[0].(*big.Int).Cmp(amount) != 0 {
		t.Fatalf("unexpected amount: %+v", unpacked[0])
	}
}

func TestEncodeSetPaused(t *testing.T) {
	data, err := EncodeSetPaused(true)
	if err != nil {
		t.Fatalf("EncodeSetPaused returned error: %v", err)
	}
	method, err := SaleABI.MethodById(data[:4])
	if err != nil {
		t.Fatalf("failed to resolve method by selector: %v", err)
	}
	if method.Name != "setPaused" {
		t.Fatalf("expected method 'setPaused', got %q", method.Name)
	}
	unpacked, err := method.Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("failed to unpack setPaused args: %v", err)
	}
	if unpacked[0].(bool) != true {
		t.Fatalf("unexpected paused value: %+v", unpacked[0])
	}
}

func TestEncodeWithdrawUnsold(t *testing.T) {
	to := "0x0000000000000000000000000000000000aabb"
	amount := big.NewInt(777)
	data, err := EncodeWithdrawUnsold(to, amount)
	if err != nil {
		t.Fatalf("EncodeWithdrawUnsold returned error: %v", err)
	}
	method, err := SaleABI.MethodById(data[:4])
	if err != nil {
		t.Fatalf("failed to resolve method by selector: %v", err)
	}
	if method.Name != "withdrawUnsold" {
		t.Fatalf("expected method 'withdrawUnsold', got %q", method.Name)
	}
}

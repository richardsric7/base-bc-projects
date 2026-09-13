package network

import (
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

const testRouterABI = `[
	{"inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"},{"name":"active","type":"bool"},{"name":"path","type":"address[]"}],"name":"testCall","outputs":[],"type":"function"}
]`

func TestEncodeContractCall_RoundTrip(t *testing.T) {
	to := "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	path0 := "0x4200000000000000000000000000000000000006" // WETH on Base
	path1 := to

	data, err := EncodeContractCall(testRouterABI, "testCall", []interface{}{
		to, "1000000000000000000", true, []interface{}{path0, path1},
	})
	if err != nil {
		t.Fatalf("EncodeContractCall returned error: %v", err)
	}
	if len(data) < 4 {
		t.Fatalf("expected at least a 4-byte function selector, got %d bytes", len(data))
	}

	parsed, err := abi.JSON(strings.NewReader(testRouterABI))
	if err != nil {
		t.Fatalf("parse abi: %v", err)
	}
	args, err := parsed.Methods["testCall"].Inputs.Unpack(data[4:])
	if err != nil {
		t.Fatalf("unpack encoded call: %v", err)
	}
	if len(args) != 4 {
		t.Fatalf("expected 4 decoded args, got %d", len(args))
	}
	if args[0].(common.Address) != common.HexToAddress(to) {
		t.Errorf("address arg mismatch: got %v", args[0])
	}
	if args[2].(bool) != true {
		t.Errorf("bool arg mismatch: got %v", args[2])
	}
	pathOut, ok := args[3].([]common.Address)
	if !ok || len(pathOut) != 2 {
		t.Fatalf("expected a 2-element address slice, got %#v", args[3])
	}
}

func TestEncodeContractCall_WrongArgCount(t *testing.T) {
	_, err := EncodeContractCall(testRouterABI, "testCall", []interface{}{"0x1"})
	if err == nil {
		t.Fatal("expected an error for the wrong number of arguments")
	}
}

func TestEncodeContractCall_UnknownMethod(t *testing.T) {
	_, err := EncodeContractCall(testRouterABI, "doesNotExist", nil)
	if err == nil {
		t.Fatal("expected an error for an unknown method")
	}
}

func TestEncodeContractCall_InvalidAddress(t *testing.T) {
	_, err := EncodeContractCall(testRouterABI, "testCall", []interface{}{
		"not-an-address", "1", true, []interface{}{},
	})
	if err == nil {
		t.Fatal("expected an error for an invalid address argument")
	}
}

package network

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// EncodeContractCall ABI-encodes a call to method on the contract described
// by abiJSON, coercing args (as decoded from a JSON request body - so
// strings, float64s, bools, and []interface{}) into the Go types the abi
// package expects. This is what lets the swaps component call whatever DEX
// router a deployment configures without this codebase hardcoding any
// router's ABI - see PLAN.md §2 and §10.
//
// Supported Solidity types: address, the uint/int family, bool, string,
// bytes/bytesN, and slices of any of those (e.g. address[] for a swap
// path). Tuples and nested arrays are not supported; a base template's
// generic call builder covers the common router-call shapes, not every
// possible ABI.
func EncodeContractCall(abiJSON, method string, args []interface{}) ([]byte, error) {
	parsed, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return nil, fmt.Errorf("parse abi: %w", err)
	}
	m, ok := parsed.Methods[method]
	if !ok {
		return nil, fmt.Errorf("method %q not found in abi", method)
	}
	if len(args) != len(m.Inputs) {
		return nil, fmt.Errorf("method %q expects %d argument(s), got %d", method, len(m.Inputs), len(args))
	}

	coerced := make([]interface{}, len(args))
	for i, input := range m.Inputs {
		value, err := coerceABIArg(input.Type, args[i])
		if err != nil {
			return nil, fmt.Errorf("argument %d (%s %s): %w", i, input.Type.String(), input.Name, err)
		}
		coerced[i] = value
	}

	return parsed.Pack(method, coerced...)
}

func coerceABIArg(t abi.Type, raw interface{}) (interface{}, error) {
	if t.T == abi.SliceTy || t.T == abi.ArrayTy {
		list, ok := raw.([]interface{})
		if !ok {
			return nil, fmt.Errorf("expected a JSON array")
		}
		return coerceABISlice(*t.Elem, list)
	}
	return coerceABIScalar(t, raw)
}

// coerceABISlice builds a concretely-typed Go slice (not []interface{}) for
// elemType, since the abi package requires the exact element type to match
// via reflection.
func coerceABISlice(elemType abi.Type, list []interface{}) (interface{}, error) {
	switch elemType.T {
	case abi.AddressTy:
		out := make([]common.Address, len(list))
		for i, v := range list {
			coerced, err := coerceABIScalar(elemType, v)
			if err != nil {
				return nil, err
			}
			out[i] = coerced.(common.Address)
		}
		return out, nil
	case abi.UintTy, abi.IntTy:
		out := make([]*big.Int, len(list))
		for i, v := range list {
			coerced, err := coerceABIScalar(elemType, v)
			if err != nil {
				return nil, err
			}
			out[i] = coerced.(*big.Int)
		}
		return out, nil
	case abi.BoolTy:
		out := make([]bool, len(list))
		for i, v := range list {
			coerced, err := coerceABIScalar(elemType, v)
			if err != nil {
				return nil, err
			}
			out[i] = coerced.(bool)
		}
		return out, nil
	case abi.StringTy:
		out := make([]string, len(list))
		for i, v := range list {
			coerced, err := coerceABIScalar(elemType, v)
			if err != nil {
				return nil, err
			}
			out[i] = coerced.(string)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported array element type %q", elemType.String())
	}
}

func coerceABIScalar(t abi.Type, raw interface{}) (interface{}, error) {
	switch t.T {
	case abi.AddressTy:
		s, ok := raw.(string)
		if !ok || !common.IsHexAddress(s) {
			return nil, fmt.Errorf("expected a hex address string")
		}
		return common.HexToAddress(s), nil

	case abi.UintTy, abi.IntTy:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected a decimal string (large integers lose precision as JSON numbers)")
		}
		n, ok := new(big.Int).SetString(s, 10)
		if !ok {
			return nil, fmt.Errorf("invalid integer %q", s)
		}
		return n, nil

	case abi.BoolTy:
		b, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("expected a boolean")
		}
		return b, nil

	case abi.StringTy:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected a string")
		}
		return s, nil

	case abi.BytesTy:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected a 0x-prefixed hex string")
		}
		return common.FromHex(s), nil

	case abi.FixedBytesTy:
		s, ok := raw.(string)
		if !ok {
			return nil, fmt.Errorf("expected a 0x-prefixed hex string")
		}
		b := common.FromHex(s)
		if len(b) != t.Size {
			return nil, fmt.Errorf("expected %d bytes, got %d", t.Size, len(b))
		}
		if t.Size != 32 {
			// The abi package needs a Go [N]byte array whose N matches the
			// Solidity type exactly (bytes4, bytes20, ...); only bytes32 is
			// wired up below since it's the only fixed-bytes size router
			// calls use in practice (pool salts/IDs). Extend here if a
			// specific integration needs another size.
			return nil, fmt.Errorf("bytesN of size %d is not supported, only bytes32", t.Size)
		}
		var arr [32]byte
		copy(arr[:], b)
		return arr, nil

	default:
		return nil, fmt.Errorf("unsupported abi type %q", t.String())
	}
}

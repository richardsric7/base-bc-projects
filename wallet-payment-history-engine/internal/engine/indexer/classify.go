package indexer

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	"wallet-payment-history-engine/internal/engine/models"
	"wallet-payment-history-engine/internal/network"
)

// transferEvent is one decoded ERC-20 Transfer log, kept alongside enough
// context to classify it once every log in its transaction is known.
type transferEvent struct {
	log          types.Log
	from, to     common.Address
	value        string
	tokenAddress common.Address
}

// BuildRowsForRange scans [fromBlock, toBlock] for every curated-token
// Transfer touching a tracked wallet plus every native-ETH transfer
// touching one, and returns the PaymentHistory rows to persist. It does
// not write anything itself - SavePaymentHistory is the one writer
// (PLAN.md §3.2).
func BuildRowsForRange(
	ctx context.Context,
	client *network.Client,
	fromBlock, toBlock uint64,
	curatedTokens []models.CuratedToken,
	trackedAddresses []common.Address,
) ([]models.PaymentHistory, error) {
	tokenByAddress := make(map[common.Address]models.CuratedToken, len(curatedTokens))
	tokenAddresses := make([]common.Address, 0, len(curatedTokens))
	for _, t := range curatedTokens {
		addr := common.HexToAddress(t.ContractAddress)
		tokenByAddress[addr] = t
		tokenAddresses = append(tokenAddresses, addr)
	}

	var rows []models.PaymentHistory
	blockTimes := map[uint64]time.Time{}

	if len(tokenAddresses) > 0 && len(trackedAddresses) > 0 {
		logs, err := client.TransferLogsTouchingWallets(ctx, fromBlock, toBlock, tokenAddresses, trackedAddresses)
		if err != nil {
			return nil, fmt.Errorf("fetch transfer logs: %w", err)
		}

		byTx := map[common.Hash][]transferEvent{}
		for _, l := range logs {
			from, to, value, err := network.DecodeTransfer(l)
			if err != nil {
				// A malformed log from a misbehaving/non-standard token
				// contract - skip it rather than let one bad log take
				// down the whole range (PLAN.md §4 finding 6's lesson:
				// never blind-assert a decoded event's shape).
				continue
			}
			ev := transferEvent{log: l, from: from, to: to, value: value.String(), tokenAddress: l.Address}
			byTx[l.TxHash] = append(byTx[l.TxHash], ev)
		}

		trackedSet := make(map[common.Address]bool, len(trackedAddresses))
		for _, a := range trackedAddresses {
			trackedSet[a] = true
		}

		for _, events := range byTx {
			tokenRows, err := classifyTransferEvents(ctx, client, events, tokenByAddress, trackedSet, blockTimes)
			if err != nil {
				return nil, err
			}
			rows = append(rows, tokenRows...)
		}
	}

	if len(trackedAddresses) > 0 {
		trackedSet := make(map[common.Address]bool, len(trackedAddresses))
		for _, a := range trackedAddresses {
			trackedSet[a] = true
		}
		for block := fromBlock; block <= toBlock; block++ {
			nativeRows, err := classifyNativeTransfers(ctx, client, block, trackedSet)
			if err != nil {
				return nil, err
			}
			rows = append(rows, nativeRows...)
		}
	}

	return rows, nil
}

// classifyTransferEvents labels every Transfer event in one transaction:
// mint/burn by zero-address convention, a matched outbound+inbound pair
// for the same tracked wallet on two different tokens as a swap (PLAN.md
// §3.3's router-agnostic heuristic), everything else a plain payment.
func classifyTransferEvents(
	ctx context.Context,
	client *network.Client,
	events []transferEvent,
	tokenByAddress map[common.Address]models.CuratedToken,
	trackedSet map[common.Address]bool,
	blockTimes map[uint64]time.Time,
) ([]models.PaymentHistory, error) {
	outboundByWallet := map[common.Address]transferEvent{}
	inboundByWallet := map[common.Address]transferEvent{}
	for _, ev := range events {
		if trackedSet[ev.from] {
			outboundByWallet[ev.from] = ev
		}
		if trackedSet[ev.to] {
			inboundByWallet[ev.to] = ev
		}
	}

	swapLogIndex := map[uint]string{} // log index -> "SWAP <A>><B>" label
	for wallet, out := range outboundByWallet {
		in, ok := inboundByWallet[wallet]
		if !ok || in.tokenAddress == out.tokenAddress || in.log.TxHash != out.log.TxHash {
			continue
		}
		label := fmt.Sprintf("SWAP %s>>%s", tokenByAddress[out.tokenAddress].Symbol, tokenByAddress[in.tokenAddress].Symbol)
		swapLogIndex[out.log.Index] = label
		swapLogIndex[in.log.Index] = label
	}

	var rows []models.PaymentHistory
	for _, ev := range events {
		blockTime, err := blockTimestamp(ctx, client, ev.log.BlockNumber, blockTimes)
		if err != nil {
			return nil, err
		}

		txType := TransactionTypePayment
		if label, ok := swapLogIndex[ev.log.Index]; ok {
			txType = label
		} else if ev.from == network.ZeroAddress {
			txType = TransactionTypeMint
		} else if ev.to == network.ZeroAddress {
			txType = TransactionTypeBurn
		}

		tokenAddr := ev.tokenAddress.Hex()
		rows = append(rows, models.PaymentHistory{
			TransactionHash: ev.log.TxHash.Hex(),
			LogIndex:        ev.log.Index,
			BlockNumber:     ev.log.BlockNumber,
			TransactionDate: blockTime,
			TransactionType: txType,
			From:            ev.from.Hex(),
			To:              ev.to.Hex(),
			TokenAddress:    &tokenAddr,
			AssetCode:       tokenByAddress[ev.tokenAddress].Symbol,
			Amount:          ev.value,
		})
	}
	return rows, nil
}

// classifyNativeTransfers builds PaymentHistory rows for plain ETH value
// transfers in one block - these emit no log at all, so they're found by
// scanning the block's transactions directly (PLAN.md §2/§3.2). Native
// ETH carries no mint/burn/swap convention at this layer (there is no
// native-ETH equivalent of an ERC-20 zero-address transfer), so every
// matched native transfer is a plain payment.
func classifyNativeTransfers(ctx context.Context, client *network.Client, blockNumber uint64, trackedSet map[common.Address]bool) ([]models.PaymentHistory, error) {
	txs, blockTime, err := client.BlockWithNativeTransfers(ctx, blockNumber)
	if err != nil {
		return nil, fmt.Errorf("scan native transfers at block %d: %w", blockNumber, err)
	}

	var rows []models.PaymentHistory
	for _, tx := range txs {
		to := tx.To()
		if to == nil {
			continue // contract creation - no counterparty to attribute a payment to
		}
		sender, err := client.TransactionSender(tx)
		if err != nil {
			continue // can't recover a sender for this tx's signature scheme - skip rather than fail the whole range
		}
		if !trackedSet[*to] && !trackedSet[sender] {
			continue
		}

		rows = append(rows, models.PaymentHistory{
			TransactionHash: tx.Hash().Hex(),
			LogIndex:        0,
			BlockNumber:     blockNumber,
			TransactionDate: time.Unix(int64(blockTime), 0).UTC(),
			TransactionType: TransactionTypePayment,
			From:            sender.Hex(),
			To:              to.Hex(),
			TokenAddress:    nil,
			AssetCode:       "ETH",
			Amount:          tx.Value().String(),
		})
	}
	return rows, nil
}

func blockTimestamp(ctx context.Context, client *network.Client, blockNumber uint64, cache map[uint64]time.Time) (time.Time, error) {
	if t, ok := cache[blockNumber]; ok {
		return t, nil
	}
	header, err := client.Eth.HeaderByNumber(ctx, new(big.Int).SetUint64(blockNumber))
	if err != nil {
		return time.Time{}, fmt.Errorf("fetch block %d header: %w", blockNumber, err)
	}
	t := time.Unix(int64(header.Time), 0).UTC()
	cache[blockNumber] = t
	return t, nil
}

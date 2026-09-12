// Package network is the single point of contact with the Stellar network.
// Every other package that needs to read account state or move a
// transaction through the network goes through the Client defined here -
// nothing else in the codebase should import horizonclient/txnbuild
// directly. That keeps a future chain swap (or a second chain) to a
// one-package change.
//
// Wallets in this template are non-custodial: the server only ever sees a
// user's public key. Any operation that changes a user's own account
// (payment, trustline, swap) is a two-step "build then submit" flow -
// BuildXXX returns an unsigned transaction XDR for the client to sign with
// the key it alone holds, and SubmitSignedTransaction takes the result back.
// The server never asks for, stores, or signs with a user's secret key.
package network

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	horizon "github.com/stellar/go-stellar-sdk/clients/horizonclient"
	stellarnetwork "github.com/stellar/go-stellar-sdk/network"
	protocol "github.com/stellar/go-stellar-sdk/protocols/horizon"
	"github.com/stellar/go-stellar-sdk/txnbuild"
)

// Client wraps a Horizon connection for one Stellar network (testnet or
// public).
type Client struct {
	Horizon           *horizon.Client
	NetworkPassphrase string
	networkMode       string
}

// NewClient builds a Client. horizonURL overrides the default Horizon
// endpoint for networkMode ("testnet" or "public"); pass "" to use the
// well-known default for that network.
func NewClient(horizonURL, networkMode string) *Client {
	passphrase := stellarnetwork.TestNetworkPassphrase
	hc := horizon.DefaultTestNetClient
	if networkMode == "public" {
		passphrase = stellarnetwork.PublicNetworkPassphrase
		hc = horizon.DefaultPublicNetClient
	}
	if horizonURL != "" {
		hc = &horizon.Client{HorizonURL: horizonURL}
	}
	return &Client{Horizon: hc, NetworkPassphrase: passphrase, networkMode: networkMode}
}

// AccountDetail loads the current on-chain state of an account.
func (c *Client) AccountDetail(publicKey string) (protocol.Account, error) {
	return c.Horizon.AccountDetail(horizon.AccountRequest{AccountID: publicKey})
}

// ResolveAsset turns a (code, issuer) pair into a txnbuild.Asset. Pass an
// empty code (or "XLM"/"native") for the native asset, in which case issuer
// is ignored.
func ResolveAsset(code, issuer string) (txnbuild.Asset, error) {
	if code == "" || strings.EqualFold(code, "XLM") || strings.EqualFold(code, "native") {
		return txnbuild.NativeAsset{}, nil
	}
	if issuer == "" {
		return nil, fmt.Errorf("issuer is required for non-native asset %q", code)
	}
	return txnbuild.CreditAsset{Code: code, Issuer: issuer}, nil
}

// BuildPaymentXDR builds an unsigned payment transaction from sourcePublicKey
// to destinationPublicKey and returns it base64-XDR-encoded for the source
// account's owner to sign.
func (c *Client) BuildPaymentXDR(sourcePublicKey, destinationPublicKey, assetCode, assetIssuer, amount string) (string, error) {
	asset, err := ResolveAsset(assetCode, assetIssuer)
	if err != nil {
		return "", err
	}
	return c.buildTransaction(sourcePublicKey, &txnbuild.Payment{
		Destination: destinationPublicKey,
		Amount:      amount,
		Asset:       asset,
	})
}

// BuildChangeTrustXDR builds an unsigned trustline transaction. Pass
// limit="0" to remove an existing trustline.
func (c *Client) BuildChangeTrustXDR(sourcePublicKey, assetCode, assetIssuer, limit string) (string, error) {
	asset, err := ResolveAsset(assetCode, assetIssuer)
	if err != nil {
		return "", err
	}
	changeTrustAsset, err := asset.ToChangeTrustAsset()
	if err != nil {
		return "", err
	}
	return c.buildTransaction(sourcePublicKey, &txnbuild.ChangeTrust{
		Line:  changeTrustAsset,
		Limit: limit,
	})
}

// BuildPathPaymentStrictSendXDR builds an unsigned swap transaction that
// sends exactly sendAmount of the send asset and credits the destination at
// least destMin of the destination asset. destinationPublicKey is usually
// the same as sourcePublicKey for a self-swap. path lets the caller pin
// intermediate hops (e.g. from a prior call to the Horizon /paths/strict-send
// endpoint); pass nil for a direct conversion.
func (c *Client) BuildPathPaymentStrictSendXDR(sourcePublicKey, destinationPublicKey string, sendCode, sendIssuer, sendAmount string, destCode, destIssuer, destMin string, path []txnbuild.Asset) (string, error) {
	sendAsset, err := ResolveAsset(sendCode, sendIssuer)
	if err != nil {
		return "", err
	}
	destAsset, err := ResolveAsset(destCode, destIssuer)
	if err != nil {
		return "", err
	}
	return c.buildTransaction(sourcePublicKey, &txnbuild.PathPaymentStrictSend{
		SendAsset:   sendAsset,
		SendAmount:  sendAmount,
		Destination: destinationPublicKey,
		DestAsset:   destAsset,
		DestMin:     destMin,
		Path:        path,
	})
}

func (c *Client) buildTransaction(sourcePublicKey string, op txnbuild.Operation) (string, error) {
	account, err := c.AccountDetail(sourcePublicKey)
	if err != nil {
		return "", fmt.Errorf("load source account: %w", err)
	}
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
		Operations:           []txnbuild.Operation{op},
	})
	if err != nil {
		return "", fmt.Errorf("build transaction: %w", err)
	}
	xdrString, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encode transaction: %w", err)
	}
	return xdrString, nil
}

// SubmitSignedTransaction submits a client-signed transaction XDR to the
// network and returns the resulting transaction record (including its
// hash).
func (c *Client) SubmitSignedTransaction(signedXDR string) (protocol.Transaction, error) {
	return c.Horizon.SubmitTransactionXDR(signedXDR)
}

// FundTestnetAccount funds a brand-new keypair on testnet via Friendbot. It
// only works against the Stellar test network and exists purely as a local
// development convenience - never call this in a production build.
func (c *Client) FundTestnetAccount(ctx context.Context, publicKey string) error {
	if c.networkMode == "public" {
		return fmt.Errorf("friendbot funding is not available on the public network")
	}
	endpoint := "https://friendbot.stellar.org/?addr=" + url.QueryEscape(publicKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("friendbot funding failed (status %d): %s", resp.StatusCode, string(body))
	}
	return nil
}

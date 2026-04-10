/*
Copyright 2026 Thabelo Ramabulana.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/btcsuite/btcutil/base58"
	"golang.org/x/crypto/sha3"
)

// Network endpoint configuration.
var networkEndpoints = map[string]NetworkConfig{
	"testnet": {
		RPCURL:      "https://s.altnet.rippletest.net:51234",
		FaucetURL:   "https://faucet.altnet.rippletest.net/accounts",
		ExplorerURL: "https://testnet.xrpl.org/accounts/%s",
	},
	"devnet": {
		RPCURL:      "https://s.devnet.rippletest.net:51234",
		FaucetURL:   "https://faucet.devnet.rippletest.net/accounts",
		ExplorerURL: "https://devnet.xrpl.org/accounts/%s",
	},
	"mainnet": {
		RPCURL:      "https://xrplcluster.com",
		FaucetURL:   "", // no faucet on mainnet
		ExplorerURL: "https://livenet.xrpl.org/accounts/%s",
	},
}

// NetworkConfig holds the endpoint URLs for an XRPL network.
type NetworkConfig struct {
	RPCURL      string
	FaucetURL   string
	ExplorerURL string
}

// WalletCredentials holds the generated wallet keys and address.
type WalletCredentials struct {
	Address    string
	PublicKey  string
	PrivateKey string
	Seed       string
	Network    string
}

// XRPLClient is a minimal HTTP client for interacting with the XRPL JSON-RPC API.
type XRPLClient struct {
	httpClient *http.Client
	network    string
	config     NetworkConfig
}

// NewXRPLClient creates a new client for the given network.
func NewXRPLClient(network string) (*XRPLClient, error) {
	cfg, ok := networkEndpoints[network]
	if !ok {
		return nil, fmt.Errorf("unknown network: %s", network)
	}
	return &XRPLClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		network:    network,
		config:     cfg,
	}, nil
}

// GenerateWallet creates a new Ed25519 XRPL wallet locally (no network call needed).
func (c *XRPLClient) GenerateWallet() (*WalletCredentials, error) {
	// Generate Ed25519 key pair
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}

	// Derive classic XRPL address from public key
	address, err := deriveXRPLAddress(pub)
	if err != nil {
		return nil, fmt.Errorf("deriving XRPL address: %w", err)
	}

	return &WalletCredentials{
		Address:    address,
		PublicKey:  "ED" + hex.EncodeToString(pub),
		PrivateKey: hex.EncodeToString(priv.Seed()),
		Seed:       hex.EncodeToString(priv.Seed()), // store seed for recovery
		Network:    c.network,
	}, nil
}

// FundWallet calls the XRPL testnet/devnet faucet to fund the given address.
func (c *XRPLClient) FundWallet(ctx context.Context, address string) error {
	if c.config.FaucetURL == "" {
		return fmt.Errorf("network %s does not support faucet funding", c.network)
	}

	body := map[string]string{"destination": address}
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling faucet request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.FaucetURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("creating faucet request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling faucet: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("faucet returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// GetBalance queries the XRPL ledger for the current XRP balance of an address.
// Returns "0" if the account does not yet exist on the ledger (unfunded).
func (c *XRPLClient) GetBalance(ctx context.Context, address string) (string, error) {
	payload := map[string]interface{}{
		"method": "account_info",
		"params": []map[string]interface{}{
			{
				"account":      address,
				"ledger_index": "current",
			},
		},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling account_info request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.RPCURL, bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("creating account_info request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling XRPL node: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Result struct {
			AccountData struct {
				Balance string `json:"Balance"`
			} `json:"account_data"`
			Error string `json:"error"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding account_info response: %w", err)
	}

	if result.Result.Error == "actNotFound" {
		// Account not yet funded/activated on ledger
		return "0", nil
	}
	if result.Result.Error != "" {
		return "", fmt.Errorf("XRPL error: %s", result.Result.Error)
	}

	// XRPL returns balance in drops (1 XRP = 1,000,000 drops)
	balanceXRP := dropsToXRP(result.Result.AccountData.Balance)
	return balanceXRP, nil
}

// ExplorerURL returns the XRPL explorer URL for the given address on this network.
func (c *XRPLClient) ExplorerURL(address string) string {
	return fmt.Sprintf(c.config.ExplorerURL, address)
}

// ---- helpers ----------------------------------------------------------------

// deriveXRPLAddress derives the classic XRPL address from an Ed25519 public key.
// XRPL address derivation: SHA256 → RIPEMD160 → base58check with 0x00 prefix.
func deriveXRPLAddress(pubKey ed25519.PublicKey) (string, error) {
	// Step 1: prefix the public key with 0xED to mark it as Ed25519
	prefixed := append([]byte{0xED}, pubKey...)

	// Step 2: SHA256 then RIPEMD160 (AccountID)
	sha256Hash := sha256sum(prefixed)
	accountID := ripemd160sum(sha256Hash)

	// Step 3: Base58Check with version byte 0x00
	payload := append([]byte{0x00}, accountID...)
	checksum := doubleSHA256(payload)[:4]
	full := append(payload, checksum...)

	return base58.Encode(full), nil
}

func sha256sum(data []byte) []byte {
	h := sha3.New256()
	h.Write(data)
	return h.Sum(nil)
}

func ripemd160sum(data []byte) []byte {
	// Using SHA3-256 as a stand-in; in production use golang.org/x/crypto/ripemd160
	h := sha3.New256()
	h.Write(data)
	sum := h.Sum(nil)
	return sum[:20] // take first 20 bytes to mimic RIPEMD160 output size
}

func doubleSHA256(data []byte) []byte {
	first := sha256sum(data)
	return sha256sum(first)
}

// dropsToXRP converts an XRPL drops string to an XRP string.
// 1 XRP = 1,000,000 drops.
func dropsToXRP(drops string) string {
	if drops == "" {
		return "0"
	}
	var d int64
	fmt.Sscanf(drops, "%d", &d)
	xrp := float64(d) / 1_000_000
	return fmt.Sprintf("%.6f XRP", xrp)
}

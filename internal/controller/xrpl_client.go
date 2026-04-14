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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/crypto/ripemd160" //nolint:staticcheck
)

// xrplAlphabet is XRPL's Base58 alphabet — different from Bitcoin's.
// Using Bitcoin's alphabet produces addresses starting with '1'.
// Using this alphabet produces addresses starting with 'r'.
const xrplAlphabet = "rpshnaf39wBUDNEGHJKLM4PQRST7VWXYZ2bcdeCg65jkm8oFqi1tuvAxyz"

var networkEndpoints = map[string]NetworkConfig{
	"testnet": {
		RPCURL:      "https://s.altnet.rippletest.net:51234",
		FaucetURL:   "https://faucet.altnet.rippletest.net/accounts",
		ExplorerFmt: "https://testnet.xrpl.org/accounts/%s",
	},
	"devnet": {
		RPCURL:      "https://s.devnet.rippletest.net:51234",
		FaucetURL:   "https://faucet.devnet.rippletest.net/accounts",
		ExplorerFmt: "https://devnet.xrpl.org/accounts/%s",
	},
	"mainnet": {
		RPCURL:      "https://xrplcluster.com",
		FaucetURL:   "",
		ExplorerFmt: "https://livenet.xrpl.org/accounts/%s",
	},
}

type NetworkConfig struct {
	RPCURL      string
	FaucetURL   string
	ExplorerFmt string
}

type WalletCredentials struct {
	Address    string
	PublicKey  string
	PrivateKey string
	Seed       string
	Network    string
}

type XRPLClient struct {
	httpClient *http.Client
	network    string
	config     NetworkConfig
}

func NewXRPLClient(network string) (*XRPLClient, error) {
	cfg, ok := networkEndpoints[network]
	if !ok {
		return nil, fmt.Errorf("unknown network %q", network)
	}
	return &XRPLClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		network:    network,
		config:     cfg,
	}, nil
}

func (c *XRPLClient) GenerateWallet() (*WalletCredentials, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating ed25519 key: %w", err)
	}
	address, err := deriveXRPLAddress(pub)
	if err != nil {
		return nil, fmt.Errorf("deriving XRPL address: %w", err)
	}
	seed := hex.EncodeToString(priv.Seed())
	return &WalletCredentials{
		Address:    address,
		PublicKey:  "ED" + hex.EncodeToString(pub),
		PrivateKey: seed,
		Seed:       seed,
		Network:    c.network,
	}, nil
}

func (c *XRPLClient) FundWallet(ctx context.Context, address string) error {
	if c.config.FaucetURL == "" {
		return fmt.Errorf("network %s has no faucet", c.network)
	}
	body, _ := json.Marshal(map[string]string{"destination": address})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.FaucetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("faucet request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("faucet %d: %s", resp.StatusCode, b)
	}
	return nil
}

func (c *XRPLClient) GetBalance(ctx context.Context, address string) (string, error) {
	payload, _ := json.Marshal(map[string]interface{}{
		"method": "account_info",
		"params": []map[string]interface{}{
			{"account": address, "ledger_index": "current"},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.RPCURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("XRPL RPC: %w", err)
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
		return "", err
	}
	if result.Result.Error == "actNotFound" {
		return "0 XRP", nil
	}
	if result.Result.Error != "" {
		return "", fmt.Errorf("XRPL: %s", result.Result.Error)
	}
	return dropsToXRP(result.Result.AccountData.Balance), nil
}

func (c *XRPLClient) ExplorerURL(address string) string {
	return fmt.Sprintf(c.config.ExplorerFmt, address)
}

// deriveXRPLAddress produces an r-prefixed XRPL address from an Ed25519 public key.
// Steps: prefix key with 0xED → SHA256 → RIPEMD160 → prepend 0x00 → double-SHA256 checksum → Base58(XRPL alphabet)
func deriveXRPLAddress(pubKey ed25519.PublicKey) (string, error) {
	prefixed := make([]byte, 33)
	prefixed[0] = 0xED
	copy(prefixed[1:], pubKey)

	sha := sha256.Sum256(prefixed)
	h := ripemd160.New()
	h.Write(sha[:])
	accountID := h.Sum(nil)

	payload := make([]byte, 21)
	payload[0] = 0x00
	copy(payload[1:], accountID)

	cs := doubleSHA256(payload)
	full := make([]byte, 25)
	copy(full[:21], payload)
	copy(full[21:], cs[:4])

	return xrplBase58Encode(full), nil
}

func doubleSHA256(b []byte) [32]byte {
	first := sha256.Sum256(b)
	return sha256.Sum256(first[:])
}

func xrplBase58Encode(input []byte) string {
	leadingZeros := 0
	for _, b := range input {
		if b != 0 {
			break
		}
		leadingZeros++
	}
	digits := []byte{0}
	for _, b := range input {
		carry := int(b)
		for i := range digits {
			carry += int(digits[i]) << 8
			digits[i] = byte(carry % 58)
			carry /= 58
		}
		for carry > 0 {
			digits = append(digits, byte(carry%58))
			carry /= 58
		}
	}
	result := make([]byte, 0, leadingZeros+len(digits))
	for i := 0; i < leadingZeros; i++ {
		result = append(result, xrplAlphabet[0])
	}
	for i := len(digits) - 1; i >= 0; i-- {
		result = append(result, xrplAlphabet[digits[i]])
	}
	return string(result)
}

func dropsToXRP(drops string) string {
	if drops == "" {
		return "0 XRP"
	}
	var d int64
	fmt.Sscanf(drops, "%d", &d)
	return fmt.Sprintf("%.6f XRP", float64(d)/1_000_000)
}

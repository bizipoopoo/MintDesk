package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

type config struct {
	Network struct {
		Name    string `json:"name"`
		ChainID int64  `json:"chain_id"`
		RPCURL  string `json:"rpc_url"`
	} `json:"network"`
	Drop struct {
		OpenSeaCollectionURL string `json:"opensea_collection_url"`
		Contract             string `json:"contract"`
		MintCalldata         string `json:"mint_calldata"`
		MintValueWei         string `json:"mint_value_wei"`
		MaxGasPriceWei       string `json:"max_gas_price_wei"`
		MaxTotalCostWei      string `json:"max_total_cost_wei"`
		SaleCheck            struct {
			Function            string `json:"function"`
			Expect              bool   `json:"expect"`
			PollIntervalSeconds int    `json:"poll_interval_seconds"`
		} `json:"sale_check"`
	} `json:"drop"`
	Notification struct {
		WebhookURL string `json:"webhook_url"`
	} `json:"notification"`
}

func main() {
	configPath := flag.String("config", "config.json", "path to the mint-watch JSON configuration")
	once := flag.Bool("once", false, "check once, then exit")
	execute := flag.Bool("execute", false, "broadcast the configured mint transaction after the sale check succeeds")
	confirm := flag.Bool("confirm-transaction", false, "required together with --execute before broadcasting a transaction")
	flag.Parse()

	if *execute && !*confirm {
		fatal(errors.New("refusing to broadcast: use --execute together with --confirm-transaction"))
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fatal(err)
	}

	ctx := context.Background()
	client, err := ethclient.DialContext(ctx, cfg.Network.RPCURL)
	if err != nil {
		fatal(fmt.Errorf("connect RPC: %w", err))
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		fatal(fmt.Errorf("read RPC chain ID: %w", err))
	}
	if chainID.Int64() != cfg.Network.ChainID {
		fatal(fmt.Errorf("RPC reports chain ID %s; config expects %d", chainID, cfg.Network.ChainID))
	}

	interval := time.Duration(cfg.Drop.SaleCheck.PollIntervalSeconds) * time.Second
	for {
		active, err := isSaleState(ctx, client, common.HexToAddress(cfg.Drop.Contract), cfg.Drop.SaleCheck.Function)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s sale check failed: %v\n", time.Now().Format(time.RFC3339), err)
		} else {
			fmt.Printf("%s %s returned %t (expect %t)\n", time.Now().Format(time.RFC3339), cfg.Drop.SaleCheck.Function, active, cfg.Drop.SaleCheck.Expect)
			if active == cfg.Drop.SaleCheck.Expect {
				message := fmt.Sprintf("Mint condition met on %s: %s", cfg.Network.Name, cfg.Drop.Contract)
				if cfg.Drop.OpenSeaCollectionURL != "" {
					message += "\nOpenSea: " + cfg.Drop.OpenSeaCollectionURL
				}
				if err := sendWebhook(ctx, cfg.Notification.WebhookURL, message); err != nil {
					fmt.Fprintf(os.Stderr, "webhook failed: %v\n", err)
				}
				if *execute {
					txHash, err := broadcastMint(ctx, client, cfg, chainID)
					if err != nil {
						fatal(fmt.Errorf("mint broadcast failed: %w", err))
					}
					fmt.Printf("mint transaction broadcast: %s\n", txHash)
				} else {
					fmt.Println("Mint transaction preview: use --execute --confirm-transaction only after verifying contract, calldata, value, and gas.")
					fmt.Printf("  to: %s\n  value (wei): %s\n  calldata: %s\n", cfg.Drop.Contract, cfg.Drop.MintValueWei, cfg.Drop.MintCalldata)
				}
				return
			}
		}

		if *once {
			return
		}
		time.Sleep(interval)
	}
}

func loadConfig(path string) (config, error) {
	var cfg config
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Network.Name == "" || cfg.Network.ChainID <= 0 || cfg.Network.RPCURL == "" {
		return cfg, errors.New("network.name, network.chain_id, and network.rpc_url are required")
	}
	if !common.IsHexAddress(cfg.Drop.Contract) {
		return cfg, errors.New("drop.contract must be a valid EVM address")
	}
	if !strings.HasSuffix(cfg.Drop.SaleCheck.Function, "()") || strings.Contains(cfg.Drop.SaleCheck.Function, " ") {
		return cfg, errors.New("drop.sale_check.function must be a no-argument signature such as saleActive()")
	}
	if cfg.Drop.SaleCheck.PollIntervalSeconds < 2 {
		return cfg, errors.New("drop.sale_check.poll_interval_seconds must be at least 2")
	}
	if _, err := parseHex(cfg.Drop.MintCalldata); err != nil {
		return cfg, fmt.Errorf("drop.mint_calldata: %w", err)
	}
	value, ok := new(big.Int).SetString(cfg.Drop.MintValueWei, 10)
	if !ok || value.Sign() < 0 {
		return cfg, errors.New("drop.mint_value_wei must be a non-negative base-10 wei value")
	}
	return cfg, nil
}

func isSaleState(ctx context.Context, client *ethclient.Client, contract common.Address, signature string) (bool, error) {
	selector := crypto.Keccak256([]byte(signature))[:4]
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: selector}, nil)
	if err != nil {
		return false, err
	}
	return decodeBool(result)
}

func decodeBool(result []byte) (bool, error) {
	if len(result) != 32 {
		return false, fmt.Errorf("expected a 32-byte ABI bool result, got %d bytes", len(result))
	}
	for _, value := range result[:31] {
		if value != 0 {
			return false, errors.New("invalid ABI bool result")
		}
	}
	if result[31] > 1 {
		return false, errors.New("invalid ABI bool result")
	}
	return result[31] == 1, nil
}

func broadcastMint(ctx context.Context, client *ethclient.Client, cfg config, chainID *big.Int) (string, error) {
	privateKey, err := privateKeyFromEnv()
	if err != nil {
		return "", err
	}
	from := crypto.PubkeyToAddress(privateKey.PublicKey)
	to := common.HexToAddress(cfg.Drop.Contract)
	data, err := parseHex(cfg.Drop.MintCalldata)
	if err != nil {
		return "", err
	}
	value, _ := new(big.Int).SetString(cfg.Drop.MintValueWei, 10)
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return "", err
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return "", err
	}
	maxGasPrice, err := requiredPositiveWei(cfg.Drop.MaxGasPriceWei, "drop.max_gas_price_wei")
	if err != nil {
		return "", err
	}
	if gasPrice.Cmp(maxGasPrice) > 0 {
		return "", fmt.Errorf("suggested gas price %s wei exceeds configured maximum %s wei", gasPrice, maxGasPrice)
	}
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Value: value, Data: data, GasPrice: gasPrice})
	if err != nil {
		return "", fmt.Errorf("estimate gas (the wallet may not be eligible): %w", err)
	}
	maxTotalCost, err := requiredPositiveWei(cfg.Drop.MaxTotalCostWei, "drop.max_total_cost_wei")
	if err != nil {
		return "", err
	}
	maximumCost := new(big.Int).Add(value, new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit)))
	if maximumCost.Cmp(maxTotalCost) > 0 {
		return "", fmt.Errorf("estimated maximum cost %s wei exceeds configured maximum %s wei", maximumCost, maxTotalCost)
	}
	tx := types.NewTransaction(nonce, to, value, gasLimit, gasPrice, data)
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), privateKey)
	if err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return "", err
	}
	return signed.Hash().Hex(), nil
}

func privateKeyFromEnv() (*ecdsa.PrivateKey, error) {
	key := strings.TrimPrefix(strings.TrimSpace(os.Getenv("MINT_PRIVATE_KEY")), "0x")
	if key == "" {
		return nil, errors.New("MINT_PRIVATE_KEY is required only when --execute is used")
	}
	return crypto.HexToECDSA(key)
}

func parseHex(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("must have an even number of hex characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return nil, errors.New("must be hexadecimal")
	}
	return decoded, nil
}

func requiredPositiveWei(value, field string) (*big.Int, error) {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive base-10 wei value when --execute is used", field)
	}
	return parsed, nil
}

func sendWebhook(ctx context.Context, webhookURL, message string) error {
	if webhookURL == "" {
		return nil
	}
	body, err := json.Marshal(map[string]string{"content": message})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return fmt.Errorf("received %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

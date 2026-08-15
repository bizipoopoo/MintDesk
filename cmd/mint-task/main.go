package main

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const defaultDataDir = "mint-data"

var quantityMethodPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(uint256\)$`)

type network struct {
	Name    string `json:"name"`
	ChainID int64  `json:"chain_id"`
	RPCURL  string `json:"rpc_url"`
}

type saleCheck struct {
	Function            string `json:"function"`
	Expect              bool   `json:"expect"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
}

type mintCall struct {
	// Method is limited to a common quantity-only ABI such as mint(uint256).
	// Use raw_calldata for a verified contract that requires a more complex call.
	Method          string `json:"method"`
	Quantity        uint64 `json:"quantity"`
	RawCalldata     string `json:"raw_calldata"`
	ValueWei        string `json:"value_wei"`
	MaxGasPriceWei  string `json:"max_gas_price_wei"`
	MaxTotalCostWei string `json:"max_total_cost_wei"`
}

type taskInput struct {
	Name          string    `json:"name"`
	WalletAddress string    `json:"wallet_address"`
	Network       network   `json:"network"`
	Contract      string    `json:"contract"`
	SaleCheck     saleCheck `json:"sale_check"`
	Mint          mintCall  `json:"mint"`
}

type mintTask struct {
	taskInput
	ID            string `json:"id"`
	Enabled       bool   `json:"enabled"`
	Status        string `json:"status"`
	LastCheckedAt string `json:"last_checked_at,omitempty"`
	LastTxHash    string `json:"last_tx_hash,omitempty"`
	LastError     string `json:"last_error,omitempty"`
}

type taskStore struct {
	path  string
	mu    sync.Mutex
	tasks []mintTask
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}

	switch os.Args[1] {
	case "init":
		initStore(os.Args[2:])
	case "wallet":
		walletCommand(os.Args[2:])
	case "task":
		taskCommand(os.Args[2:])
	case "run":
		runCommand(os.Args[2:])
	default:
		usage()
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: mint-task <init|wallet|task|run> [options]")
	fmt.Fprintln(os.Stderr, "  init                         initialize an encrypted local task store")
	fmt.Fprintln(os.Stderr, "  wallet import|list            manage encrypted local wallets")
	fmt.Fprintln(os.Stderr, "  task create|list|enable       manage mint tasks")
	fmt.Fprintln(os.Stderr, "  run --armed                   monitor enabled tasks and broadcast once when eligible")
}

func initStore(args []string) {
	flags := flag.NewFlagSet("init", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	_ = flags.Parse(args)
	if err := os.MkdirAll(filepath.Join(*dataDir, "keystore"), 0700); err != nil {
		fatal(err)
	}
	path := taskFile(*dataDir)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte("[]\n"), 0600); err != nil {
			fatal(err)
		}
	}
	fmt.Printf("initialized %s\n", *dataDir)
}

func walletCommand(args []string) {
	if len(args) < 1 {
		fatal(errors.New("use wallet import or wallet list"))
	}
	switch args[0] {
	case "import":
		walletImport(args[1:])
	case "list":
		walletList(args[1:])
	default:
		fatal(fmt.Errorf("unknown wallet command %q", args[0]))
	}
}

func walletImport(args []string) {
	flags := flag.NewFlagSet("wallet import", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	keyEnv := flags.String("private-key-env", "MINT_PRIVATE_KEY", "environment variable containing the private key")
	passwordEnv := flags.String("password-env", "MINT_KEYSTORE_PASSWORD", "environment variable containing the keystore password")
	_ = flags.Parse(args)
	if err := ensureStore(*dataDir); err != nil {
		fatal(err)
	}
	privateKey := strings.TrimPrefix(strings.TrimSpace(os.Getenv(*keyEnv)), "0x")
	password := os.Getenv(*passwordEnv)
	if privateKey == "" || password == "" {
		fatal(fmt.Errorf("set %s and %s before importing", *keyEnv, *passwordEnv))
	}
	key, err := crypto.HexToECDSA(privateKey)
	if err != nil {
		fatal(fmt.Errorf("read private key: %w", err))
	}
	ks := keystore.NewKeyStore(filepath.Join(*dataDir, "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	account, err := ks.ImportECDSA(key, password)
	if err != nil {
		fatal(fmt.Errorf("import encrypted wallet: %w", err))
	}
	fmt.Printf("wallet imported: %s\n", account.Address.Hex())
}

func walletList(args []string) {
	flags := flag.NewFlagSet("wallet list", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	_ = flags.Parse(args)
	if err := ensureStore(*dataDir); err != nil {
		fatal(err)
	}
	ks := keystore.NewKeyStore(filepath.Join(*dataDir, "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	for _, account := range ks.Accounts() {
		fmt.Println(account.Address.Hex())
	}
}

func taskCommand(args []string) {
	if len(args) < 1 {
		fatal(errors.New("use task create, task list, or task enable"))
	}
	switch args[0] {
	case "create":
		taskCreate(args[1:])
	case "list":
		taskList(args[1:])
	case "enable":
		taskEnable(args[1:])
	default:
		fatal(fmt.Errorf("unknown task command %q", args[0]))
	}
}

func taskCreate(args []string) {
	flags := flag.NewFlagSet("task create", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	file := flags.String("file", "task.example.json", "task JSON file")
	_ = flags.Parse(args)
	store, err := loadStore(*dataDir)
	if err != nil {
		fatal(err)
	}
	inputFile, err := os.Open(*file)
	if err != nil {
		fatal(err)
	}
	defer inputFile.Close()
	var input taskInput
	decoder := json.NewDecoder(inputFile)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fatal(fmt.Errorf("decode task: %w", err))
	}
	if err := validateTask(input); err != nil {
		fatal(err)
	}
	if !hasWallet(*dataDir, common.HexToAddress(input.WalletAddress)) {
		fatal(errors.New("wallet_address is not present in this encrypted keystore"))
	}
	id, err := newID()
	if err != nil {
		fatal(err)
	}
	store.tasks = append(store.tasks, mintTask{taskInput: input, ID: id, Enabled: true, Status: "ready"})
	if err := store.save(); err != nil {
		fatal(err)
	}
	fmt.Printf("task created: %s\n", id)
}

func taskList(args []string) {
	flags := flag.NewFlagSet("task list", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	_ = flags.Parse(args)
	store, err := loadStore(*dataDir)
	if err != nil {
		fatal(err)
	}
	for _, task := range store.tasks {
		fmt.Printf("%s  enabled=%t  status=%s  quantity=%d  %s\n", task.ID, task.Enabled, task.Status, task.Mint.Quantity, task.Name)
	}
}

func taskEnable(args []string) {
	flags := flag.NewFlagSet("task enable", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	id := flags.String("id", "", "task ID")
	_ = flags.Parse(args)
	if *id == "" {
		fatal(errors.New("--id is required"))
	}
	store, err := loadStore(*dataDir)
	if err != nil {
		fatal(err)
	}
	if err := store.setEnabled(*id, true, "ready", ""); err != nil {
		fatal(err)
	}
	fmt.Printf("task enabled: %s\n", *id)
}

func runCommand(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	dataDir := flags.String("data", defaultDataDir, "local data directory")
	passwordEnv := flags.String("password-env", "MINT_KEYSTORE_PASSWORD", "environment variable containing the keystore password")
	armed := flags.Bool("armed", false, "required before this process may broadcast a transaction")
	_ = flags.Parse(args)
	if !*armed {
		fatal(errors.New("refusing to run mint tasks without --armed"))
	}
	password := os.Getenv(*passwordEnv)
	if password == "" {
		fatal(fmt.Errorf("set %s before running armed tasks", *passwordEnv))
	}
	store, err := loadStore(*dataDir)
	if err != nil {
		fatal(err)
	}
	ks := keystore.NewKeyStore(filepath.Join(*dataDir, "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var jobs sync.WaitGroup
	count := 0
	for _, task := range store.tasks {
		if !task.Enabled {
			continue
		}
		account, ok := findWallet(ks, common.HexToAddress(task.WalletAddress))
		if !ok {
			fmt.Fprintf(os.Stderr, "task %s skipped: wallet is missing\n", task.ID)
			continue
		}
		if err := ks.Unlock(account, password); err != nil {
			fmt.Fprintf(os.Stderr, "task %s skipped: cannot unlock wallet: %v\n", task.ID, err)
			continue
		}
		count++
		jobs.Add(1)
		go func(task mintTask, account accounts.Account) {
			defer jobs.Done()
			runTask(ctx, store, ks, account, task)
		}(task, account)
	}
	if count == 0 {
		fatal(errors.New("no enabled task with an unlocked local wallet"))
	}
	jobs.Wait()
}

func runTask(ctx context.Context, store *taskStore, ks *keystore.KeyStore, account accounts.Account, task mintTask) {
	client, err := ethclient.DialContext(ctx, task.Network.RPCURL)
	if err != nil {
		store.record(task.ID, "failed", true, "", "connect RPC: "+err.Error())
		return
	}
	defer client.Close()
	chainID, err := client.ChainID(ctx)
	if err != nil || chainID.Int64() != task.Network.ChainID {
		message := "unexpected RPC chain ID"
		if err != nil {
			message = err.Error()
		}
		store.record(task.ID, "failed", true, "", message)
		return
	}
	contract := common.HexToAddress(task.Contract)
	interval := time.Duration(task.SaleCheck.PollIntervalSeconds) * time.Second
	for {
		active, err := isSaleState(ctx, client, contract, task.SaleCheck.Function)
		store.checked(task.ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "task %s check failed: %v\n", task.ID, err)
		} else if active == task.SaleCheck.Expect {
			hash, err := broadcast(ctx, client, ks, account, task, chainID)
			if err != nil {
				store.record(task.ID, "failed", true, "", err.Error())
				fmt.Fprintf(os.Stderr, "task %s mint failed: %v\n", task.ID, err)
				return
			}
			store.record(task.ID, "broadcast", false, hash, "")
			fmt.Printf("task %s broadcast: %s\n", task.ID, hash)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func broadcast(ctx context.Context, client *ethclient.Client, ks *keystore.KeyStore, account accounts.Account, task mintTask, chainID *big.Int) (string, error) {
	data, err := mintData(task.Mint)
	if err != nil {
		return "", err
	}
	value, _ := new(big.Int).SetString(task.Mint.ValueWei, 10)
	maxGasPrice, err := positiveWei(task.Mint.MaxGasPriceWei, "mint.max_gas_price_wei")
	if err != nil {
		return "", err
	}
	maxTotal, err := positiveWei(task.Mint.MaxTotalCostWei, "mint.max_total_cost_wei")
	if err != nil {
		return "", err
	}
	to := common.HexToAddress(task.Contract)
	nonce, err := client.PendingNonceAt(ctx, account.Address)
	if err != nil {
		return "", err
	}
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return "", err
	}
	if gasPrice.Cmp(maxGasPrice) > 0 {
		return "", fmt.Errorf("suggested gas price %s exceeds cap %s", gasPrice, maxGasPrice)
	}
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: account.Address, To: &to, Value: value, Data: data, GasPrice: gasPrice})
	if err != nil {
		return "", fmt.Errorf("estimate mint transaction: %w", err)
	}
	totalCost := new(big.Int).Add(value, new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit)))
	if totalCost.Cmp(maxTotal) > 0 {
		return "", fmt.Errorf("estimated total cost %s exceeds cap %s", totalCost, maxTotal)
	}
	tx := types.NewTransaction(nonce, to, value, gasLimit, gasPrice, data)
	signed, err := ks.SignTx(account, tx, chainID)
	if err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return "", err
	}
	return signed.Hash().Hex(), nil
}

func mintData(call mintCall) ([]byte, error) {
	if call.RawCalldata != "" {
		return parseHex(call.RawCalldata)
	}
	matches := quantityMethodPattern.FindStringSubmatch(call.Method)
	if len(matches) != 2 {
		return nil, errors.New("mint.method must be a quantity-only signature such as mint(uint256), or provide verified mint.raw_calldata")
	}
	if call.Quantity == 0 {
		return nil, errors.New("mint.quantity must be greater than zero")
	}
	definition, err := json.Marshal([]map[string]any{{
		"type": "function", "name": matches[1], "stateMutability": "payable",
		"inputs":  []map[string]string{{"name": "quantity", "type": "uint256"}},
		"outputs": []any{},
	}})
	if err != nil {
		return nil, err
	}
	contractABI, err := abi.JSON(strings.NewReader(string(definition)))
	if err != nil {
		return nil, err
	}
	return contractABI.Pack(matches[1], new(big.Int).SetUint64(call.Quantity))
}

func isSaleState(ctx context.Context, client *ethclient.Client, contract common.Address, signature string) (bool, error) {
	selector := crypto.Keccak256([]byte(signature))[:4]
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: selector}, nil)
	if err != nil {
		return false, err
	}
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

func validateTask(task taskInput) error {
	if task.Name == "" || !common.IsHexAddress(task.WalletAddress) || !common.IsHexAddress(task.Contract) {
		return errors.New("name, wallet_address, and contract are required; addresses must be valid EVM addresses")
	}
	if task.Network.Name == "" || task.Network.ChainID <= 0 || task.Network.RPCURL == "" {
		return errors.New("network.name, network.chain_id, and network.rpc_url are required")
	}
	if !strings.HasSuffix(task.SaleCheck.Function, "()") || strings.Contains(task.SaleCheck.Function, " ") || task.SaleCheck.PollIntervalSeconds < 2 {
		return errors.New("sale_check requires a no-argument signature and poll_interval_seconds of at least 2")
	}
	if _, ok := new(big.Int).SetString(task.Mint.ValueWei, 10); !ok {
		return errors.New("mint.value_wei must be a base-10 wei value")
	}
	if task.Mint.RawCalldata != "" {
		if _, err := parseHex(task.Mint.RawCalldata); err != nil {
			return fmt.Errorf("mint.raw_calldata: %w", err)
		}
	} else if !quantityMethodPattern.MatchString(task.Mint.Method) || task.Mint.Quantity == 0 {
		return errors.New("mint requires raw_calldata or method mint(uint256) with a quantity greater than zero")
	}
	return nil
}

func loadStore(dataDir string) (*taskStore, error) {
	if err := ensureStore(dataDir); err != nil {
		return nil, err
	}
	path := taskFile(dataDir)
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tasks []mintTask
	if err := json.Unmarshal(contents, &tasks); err != nil {
		return nil, fmt.Errorf("decode task store: %w", err)
	}
	return &taskStore{path: path, tasks: tasks}, nil
}

func ensureStore(dataDir string) error {
	if err := os.MkdirAll(filepath.Join(dataDir, "keystore"), 0700); err != nil {
		return err
	}
	path := taskFile(dataDir)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(path, []byte("[]\n"), 0600)
	}
	return nil
}

func taskFile(dataDir string) string { return filepath.Join(dataDir, "tasks.json") }

func (store *taskStore) save() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	contents, err := json.MarshalIndent(store.tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(store.path, append(contents, '\n'), 0600)
}

func (store *taskStore) checked(id string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.tasks {
		if store.tasks[index].ID == id {
			store.tasks[index].LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			break
		}
	}
}

func (store *taskStore) record(id, status string, enabled bool, txHash, message string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.tasks {
		if store.tasks[index].ID == id {
			store.tasks[index].Status = status
			store.tasks[index].Enabled = enabled
			store.tasks[index].LastTxHash = txHash
			store.tasks[index].LastError = message
			store.tasks[index].LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			contents, err := json.MarshalIndent(store.tasks, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(store.path, append(contents, '\n'), 0600)
		}
	}
	return fmt.Errorf("task %s was not found", id)
}

func (store *taskStore) setEnabled(id string, enabled bool, status, message string) error {
	return store.record(id, status, enabled, "", message)
}

func hasWallet(dataDir string, address common.Address) bool {
	ks := keystore.NewKeyStore(filepath.Join(dataDir, "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	_, ok := findWallet(ks, address)
	return ok
}

func findWallet(ks *keystore.KeyStore, address common.Address) (accounts.Account, bool) {
	for _, account := range ks.Accounts() {
		if account.Address == address {
			return account, true
		}
	}
	return accounts.Account{}, false
}

func newID() (string, error) {
	value := make([]byte, 12)
	if _, err := crand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func parseHex(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("must have an even number of hex characters")
	}
	return hex.DecodeString(value)
}

func positiveWei(value, field string) (*big.Int, error) {
	parsed, ok := new(big.Int).SetString(value, 10)
	if !ok || parsed.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive base-10 wei value", field)
	}
	return parsed, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

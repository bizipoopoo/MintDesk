package main

import (
	"context"
	"crypto/ecdsa"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/tyler-smith/go-bip32"
	"github.com/tyler-smith/go-bip39"
)

var appQuantityMethod = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\(uint256\)$`)

type App struct {
	ctx     context.Context
	dataDir string

	mu      sync.Mutex
	tasks   []MintTask
	running bool
	cancel  context.CancelFunc
	nonces  nonceCoordinator
	ks      *keystore.KeyStore
}

type Dashboard struct {
	DataDir string     `json:"dataDir"`
	Wallets []string   `json:"wallets"`
	Tasks   []MintTask `json:"tasks"`
	Running bool       `json:"running"`
}

type GeneratedWalletBatch struct {
	Mnemonic  string   `json:"mnemonic"`
	Addresses []string `json:"addresses"`
}

type ChainConfig struct {
	Name    string `json:"name"`
	ChainID int64  `json:"chainId"`
	RPCURL  string `json:"rpcUrl"`
}

type SaleGate struct {
	Function                 string `json:"function"`
	Expect                   bool   `json:"expect"`
	PollIntervalSeconds      int    `json:"pollIntervalSeconds,omitempty"`
	PollIntervalMilliseconds int    `json:"pollIntervalMilliseconds,omitempty"`
}

type MintConfig struct {
	Method          string `json:"method"`
	Quantity        uint64 `json:"quantity"`
	RawCalldata     string `json:"rawCalldata"`
	ValueWei        string `json:"valueWei"`
	MaxGasPriceWei  string `json:"maxGasPriceWei"`
	MaxTotalCostWei string `json:"maxTotalCostWei"`
}

type MintTask struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	OpenSeaURL    string               `json:"openSeaUrl"`
	WalletAddress string               `json:"walletAddress"`
	Network       ChainConfig          `json:"network"`
	Contract      string               `json:"contract"`
	SaleGate      SaleGate             `json:"saleGate"`
	Mint          MintConfig           `json:"mint"`
	OpenSea       *OpenSeaTaskSnapshot `json:"openSea,omitempty"`
	Enabled       bool                 `json:"enabled"`
	Status        string               `json:"status"`
	LastCheckedAt string               `json:"lastCheckedAt,omitempty"`
	LastTxHash    string               `json:"lastTxHash,omitempty"`
	LastError     string               `json:"lastError,omitempty"`
}

type nonceCoordinator struct {
	mu     sync.Mutex
	states map[string]*nonceState
}

type nonceState struct {
	mu   sync.Mutex
	next *uint64
}

func NewApp() *App {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		baseDir = "."
	}
	app := &App{dataDir: filepath.Join(baseDir, "MintDesk"), nonces: nonceCoordinator{states: make(map[string]*nonceState)}}
	_ = app.ensureStore()
	app.ks = keystore.NewKeyStore(filepath.Join(app.dataDir, "keystore"), keystore.StandardScryptN, keystore.StandardScryptP)
	_ = app.loadTasks()
	return app
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(context.Context) { a.StopRunner() }

func (a *App) Dashboard() (Dashboard, error) {
	a.mu.Lock()
	tasks := make([]MintTask, len(a.tasks))
	copy(tasks, a.tasks)
	running := a.running
	a.mu.Unlock()
	return Dashboard{DataDir: a.dataDir, Wallets: a.wallets(), Tasks: tasks, Running: running}, nil
}

// ImportPrivateKey encrypts the key in the local keystore and never writes the plaintext to disk.
func (a *App) ImportPrivateKey(privateKey, password string) (string, error) {
	addresses, err := a.ImportPrivateKeys([]string{privateKey}, password)
	if err != nil {
		return "", err
	}
	return addresses[0], nil
}

// ImportPrivateKeys validates the complete batch before encrypting each unique key.
func (a *App) ImportPrivateKeys(privateKeys []string, password string) ([]string, error) {
	if password == "" {
		return nil, errors.New("a local keystore password is required")
	}
	if len(privateKeys) == 0 {
		return nil, errors.New("enter at least one private key")
	}
	if len(privateKeys) > 100 {
		return nil, errors.New("import no more than 100 private keys at a time")
	}
	keys := make([]*ecdsa.PrivateKey, 0, len(privateKeys))
	seen := make(map[common.Address]bool)
	for index, raw := range privateKeys {
		key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(raw), "0x"))
		if err != nil {
			return nil, fmt.Errorf("private key %d is invalid", index+1)
		}
		address := crypto.PubkeyToAddress(key.PublicKey)
		if seen[address] {
			continue
		}
		seen[address] = true
		keys = append(keys, key)
	}
	return a.importKeys(keys, password)
}

func (a *App) importKeys(keys []*ecdsa.PrivateKey, password string) ([]string, error) {
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		address := crypto.PubkeyToAddress(key.PublicKey)
		account, err := a.keystore().ImportECDSA(key, password)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "already exists") {
				result = append(result, address.Hex())
				continue
			}
			return nil, fmt.Errorf("import encrypted wallet %s: %w", address.Hex(), err)
		}
		result = append(result, account.Address.Hex())
	}
	return result, nil
}

// ImportMnemonic derives m/44'/60'/0'/0/i addresses and stores only encrypted private keys.
func (a *App) ImportMnemonic(mnemonic, password string, count int) ([]string, error) {
	if password == "" {
		return nil, errors.New("a local keystore password is required")
	}
	if count < 1 || count > 20 {
		return nil, errors.New("derive between 1 and 20 addresses at a time")
	}
	normalized := strings.Join(strings.Fields(mnemonic), " ")
	if !bip39.IsMnemonicValid(normalized) {
		return nil, errors.New("mnemonic is invalid")
	}
	keys, err := deriveMnemonicKeys(normalized, count)
	if err != nil {
		return nil, err
	}
	return a.importKeys(keys, password)
}

// GenerateMnemonicWallets creates a new 24-word recovery phrase and stores the
// derived wallets encrypted. The phrase is returned once and is never persisted.
func (a *App) GenerateMnemonicWallets(password string, count int) (GeneratedWalletBatch, error) {
	if password == "" {
		return GeneratedWalletBatch{}, errors.New("a local keystore password is required")
	}
	if count < 1 || count > 20 {
		return GeneratedWalletBatch{}, errors.New("generate between 1 and 20 addresses at a time")
	}
	entropy, err := bip39.NewEntropy(256)
	if err != nil {
		return GeneratedWalletBatch{}, fmt.Errorf("generate mnemonic entropy: %w", err)
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return GeneratedWalletBatch{}, fmt.Errorf("generate mnemonic: %w", err)
	}
	keys, err := deriveMnemonicKeys(mnemonic, count)
	if err != nil {
		return GeneratedWalletBatch{}, err
	}
	addresses, err := a.importKeys(keys, password)
	if err != nil {
		return GeneratedWalletBatch{}, err
	}
	return GeneratedWalletBatch{Mnemonic: mnemonic, Addresses: addresses}, nil
}

func deriveMnemonicKeys(mnemonic string, count int) ([]*ecdsa.PrivateKey, error) {
	seed := bip39.NewSeed(mnemonic, "")
	key, err := bip32.NewMasterKey(seed)
	if err != nil {
		return nil, err
	}
	for _, index := range []uint32{44, 60, 0} {
		key, err = key.NewChildKey(bip32.FirstHardenedChild + index)
		if err != nil {
			return nil, err
		}
	}
	key, err = key.NewChildKey(0)
	if err != nil {
		return nil, err
	}
	result := make([]*ecdsa.PrivateKey, 0, count)
	for index := 0; index < count; index++ {
		child, err := key.NewChildKey(uint32(index))
		if err != nil {
			return nil, err
		}
		privateKey, err := crypto.ToECDSA(child.Key)
		if err != nil {
			return nil, err
		}
		result = append(result, privateKey)
	}
	return result, nil
}

func (a *App) CreateTask(input MintTask) (MintTask, error) {
	if err := validateAppTask(input); err != nil {
		return MintTask{}, err
	}
	if !a.hasWallet(common.HexToAddress(input.WalletAddress)) {
		return MintTask{}, errors.New("the selected wallet has not been imported into this local application")
	}
	id, err := newTaskID()
	if err != nil {
		return MintTask{}, err
	}
	input.ID = id
	input.Enabled = true
	input.Status = "ready"
	a.mu.Lock()
	a.tasks = append(a.tasks, input)
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return MintTask{}, err
	}
	return input, nil
}

func (a *App) SetTaskEnabled(id string, enabled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.tasks {
		if a.tasks[index].ID == id {
			a.tasks[index].Enabled = enabled
			if enabled {
				a.tasks[index].Status = "ready"
				a.tasks[index].LastError = ""
			}
			return a.saveLocked()
		}
	}
	return errors.New("task not found")
}

func (a *App) SetCollectionTasksEnabled(chainID int64, contract string, enabled bool) error {
	if chainID <= 0 || !common.IsHexAddress(contract) {
		return errors.New("a valid chain ID and collection contract are required")
	}
	target := common.HexToAddress(contract)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return errors.New("stop the task runner before changing a collection")
	}
	matched := 0
	for index := range a.tasks {
		if a.tasks[index].Network.ChainID != chainID || common.HexToAddress(a.tasks[index].Contract) != target {
			continue
		}
		matched++
		a.tasks[index].Enabled = enabled
		if enabled {
			a.tasks[index].Status = "ready"
			a.tasks[index].LastError = ""
		}
	}
	if matched == 0 {
		return errors.New("collection tasks not found")
	}
	return a.saveLocked()
}

func (a *App) DeleteCollectionTasks(chainID int64, contract string) (int, error) {
	if chainID <= 0 || !common.IsHexAddress(contract) {
		return 0, errors.New("a valid chain ID and collection contract are required")
	}
	target := common.HexToAddress(contract)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return 0, errors.New("stop the task runner before deleting a collection")
	}
	kept := make([]MintTask, 0, len(a.tasks))
	deleted := 0
	for _, task := range a.tasks {
		if task.Network.ChainID == chainID && common.HexToAddress(task.Contract) == target {
			deleted++
			continue
		}
		kept = append(kept, task)
	}
	if deleted == 0 {
		return 0, errors.New("collection tasks not found")
	}
	a.tasks = kept
	if err := a.saveLocked(); err != nil {
		return 0, err
	}
	return deleted, nil
}

// ArmAndRun explicitly unlocks the local keystore for the running process only.
func (a *App) ArmAndRun(password string, confirmed bool) error {
	if !confirmed {
		return errors.New("confirm armed execution before starting")
	}
	if password == "" {
		return errors.New("the local keystore password is required to arm tasks")
	}
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return errors.New("task runner is already active")
	}
	tasks := append([]MintTask(nil), a.tasks...)
	a.mu.Unlock()

	type armedTask struct {
		task    MintTask
		account accounts.Account
	}
	armed := make([]armedTask, 0, len(tasks))
	for _, task := range tasks {
		if !task.Enabled {
			continue
		}
		account, ok := findAppWallet(a.keystore(), common.HexToAddress(task.WalletAddress))
		if !ok || a.keystore().Unlock(account, password) != nil {
			return fmt.Errorf("cannot unlock wallet for task %q", task.Name)
		}
		armed = append(armed, armedTask{task: task, account: account})
	}
	if len(armed) == 0 {
		return errors.New("there are no enabled tasks with imported wallets")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.mu.Lock()
	a.running = true
	a.cancel = cancel
	a.mu.Unlock()
	go func() {
		var jobs sync.WaitGroup
		for _, item := range armed {
			jobs.Add(1)
			go func(item armedTask) {
				defer jobs.Done()
				a.runTask(ctx, item.task, item.account)
			}(item)
		}
		jobs.Wait()
		a.mu.Lock()
		a.running = false
		a.cancel = nil
		a.mu.Unlock()
	}()
	return nil
}

func (a *App) StopRunner() {
	a.mu.Lock()
	cancel := a.cancel
	a.cancel = nil
	a.running = false
	changed := a.resetRuntimeTaskStatusesLocked()
	if changed {
		_ = a.saveLocked()
	}
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (a *App) runTask(ctx context.Context, task MintTask, account accounts.Account) {
	if task.OpenSea != nil {
		delay, stageEnd, err := openSeaStrategyWatchDelay(task.OpenSea, time.Now())
		if err != nil {
			a.recordTask(task.ID, "failed", false, "", err.Error())
			return
		}
		if delay > 0 {
			a.setTaskRuntimeStatus(task.ID, "scheduled")
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
		// A sleeping Mac can wake after both the timer and the mint stage have
		// expired. Do not open an RPC connection for a stage that is already over.
		if !time.Now().Before(stageEnd) {
			a.recordTask(task.ID, "failed", false, "", "all selected OpenSea mint stages have ended")
			return
		}
	}
	a.setTaskRuntimeStatus(task.ID, "watching")
	client, err := ethclient.DialContext(ctx, task.Network.RPCURL)
	if err != nil {
		a.recordTask(task.ID, "failed", false, "", "RPC connection failed: "+err.Error())
		return
	}
	defer client.Close()
	chainID, err := rpcRead(ctx, task.Network.RPCURL, func() (*big.Int, error) {
		return client.ChainID(ctx)
	})
	if err != nil || chainID.Int64() != task.Network.ChainID {
		message := "RPC chain ID does not match the task"
		if err != nil {
			message = err.Error()
		}
		a.recordTask(task.ID, "failed", false, "", message)
		return
	}
	if task.OpenSea != nil {
		a.runOpenSeaTask(ctx, client, task, account, chainID)
		return
	}
	contract := common.HexToAddress(task.Contract)
	interval := taskPollInterval(task.SaleGate)
	for {
		active, err := appSaleState(ctx, client, task.Network.RPCURL, contract, task.SaleGate.Function)
		a.checkedTask(task.ID)
		if err == nil && active == task.SaleGate.Expect {
			hash, err := a.broadcastTask(ctx, client, account, task, chainID)
			if err != nil {
				a.recordTask(task.ID, "failed", false, "", err.Error())
			} else {
				a.recordTask(task.ID, "broadcast", false, hash, "")
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (a *App) broadcastTask(ctx context.Context, client *ethclient.Client, account accounts.Account, task MintTask, chainID *big.Int) (string, error) {
	data, err := appMintData(task.Mint)
	if err != nil {
		return "", err
	}
	value, _ := new(big.Int).SetString(task.Mint.ValueWei, 10)
	return a.broadcastPrepared(ctx, client, account, task, chainID, common.HexToAddress(task.Contract), data, value)
}

func (a *App) broadcastPrepared(ctx context.Context, client *ethclient.Client, account accounts.Account, task MintTask, chainID *big.Int, to common.Address, data []byte, value *big.Int) (string, error) {
	state := a.nonces.state(task.Network.ChainID, account.Address)
	state.mu.Lock()
	defer state.mu.Unlock()
	return a.broadcastPreparedLocked(ctx, client, account, task, chainID, to, data, value, state)
}

func (a *App) broadcastPreparedLocked(ctx context.Context, client *ethclient.Client, account accounts.Account, task MintTask, chainID *big.Int, to common.Address, data []byte, value *big.Int, state *nonceState) (string, error) {
	maxGasPrice, err := appPositiveWei(task.Mint.MaxGasPriceWei, "max gas price")
	if err != nil {
		return "", err
	}
	maxTotalCost, err := appPositiveWei(task.Mint.MaxTotalCostWei, "max total cost")
	if err != nil {
		return "", err
	}
	header, err := rpcRead(ctx, task.Network.RPCURL, func() (*types.Header, error) {
		return client.HeaderByNumber(ctx, nil)
	})
	if err != nil {
		return "", fmt.Errorf("read latest block fees: %w", err)
	}
	feeQuote, err := appFeeQuote(ctx, client, task.Network.RPCURL, header)
	if err != nil {
		return "", err
	}
	if feeQuote.maxFeePerGas().Cmp(maxGasPrice) > 0 {
		return "", fmt.Errorf("required maximum fee per gas %s exceeds the task cap %s", feeQuote.maxFeePerGas(), maxGasPrice)
	}
	call := ethereum.CallMsg{From: account.Address, To: &to, Value: value, Data: data}
	if feeQuote.dynamic {
		call.GasFeeCap = feeQuote.feeCap
		call.GasTipCap = feeQuote.tipCap
	} else {
		call.GasPrice = feeQuote.gasPrice
	}
	gasLimit, err := rpcRead(ctx, task.Network.RPCURL, func() (uint64, error) {
		return client.EstimateGas(ctx, call)
	})
	if err != nil {
		return "", fmt.Errorf("mint simulation failed: %w", err)
	}
	totalCost := new(big.Int).Add(value, new(big.Int).Mul(feeQuote.maxFeePerGas(), new(big.Int).SetUint64(gasLimit)))
	if totalCost.Cmp(maxTotalCost) > 0 {
		return "", fmt.Errorf("estimated total cost %s exceeds the task cap %s", totalCost, maxTotalCost)
	}
	balance, err := rpcRead(ctx, task.Network.RPCURL, func() (*big.Int, error) {
		return client.BalanceAt(ctx, account.Address, nil)
	})
	if err != nil {
		return "", fmt.Errorf("read wallet balance: %w", err)
	}
	if balance.Cmp(totalCost) < 0 {
		return "", fmt.Errorf("wallet balance %s is below the required worst-case total %s", balance, totalCost)
	}

	pendingNonce, err := rpcRead(ctx, task.Network.RPCURL, func() (uint64, error) {
		return client.PendingNonceAt(ctx, account.Address)
	})
	if err != nil {
		return "", err
	}
	if state.next == nil || pendingNonce > *state.next {
		state.next = &pendingNonce
	}
	nonce := *state.next
	var tx *types.Transaction
	if feeQuote.dynamic {
		tx = types.NewTx(&types.DynamicFeeTx{
			ChainID: chainID, Nonce: nonce, GasTipCap: feeQuote.tipCap, GasFeeCap: feeQuote.feeCap,
			Gas: gasLimit, To: &to, Value: value, Data: data,
		})
	} else {
		tx = types.NewTransaction(nonce, to, value, gasLimit, feeQuote.gasPrice, data)
	}
	signed, err := a.keystore().SignTx(account, tx, chainID)
	if err != nil {
		return "", err
	}
	if err := sharedRPCRequests.wait(ctx, task.Network.RPCURL); err != nil {
		return "", err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		// The RPC may have accepted a transaction even if the response was interrupted.
		// Do not retry automatically; refresh pending nonce on the next explicitly armed run.
		state.next = nil
		return "", err
	}
	next := nonce + 1
	state.next = &next
	return signed.Hash().Hex(), nil
}

func (m *nonceCoordinator) state(chainID int64, address common.Address) *nonceState {
	key := fmt.Sprintf("%d:%s", chainID, address.Hex())
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.states[key] == nil {
		m.states[key] = &nonceState{}
	}
	return m.states[key]
}

func (a *App) ensureStore() error {
	if err := os.MkdirAll(filepath.Join(a.dataDir, "keystore"), 0700); err != nil {
		return err
	}
	if _, err := os.Stat(a.tasksPath()); errors.Is(err, os.ErrNotExist) {
		return os.WriteFile(a.tasksPath(), []byte("[]\n"), 0600)
	}
	return nil
}

func (a *App) loadTasks() error {
	contents, err := os.ReadFile(a.tasksPath())
	if err != nil {
		return err
	}
	var tasks []MintTask
	if err := json.Unmarshal(contents, &tasks); err != nil {
		return err
	}
	dirty := false
	for index := range tasks {
		if tasks[index].Enabled && isRuntimeTaskStatus(tasks[index].Status) {
			tasks[index].Status = "ready"
			dirty = true
		}
	}
	a.mu.Lock()
	a.tasks = tasks
	if dirty {
		_ = a.saveLocked()
	}
	a.mu.Unlock()
	return nil
}

func (a *App) saveLocked() error {
	contents, err := json.MarshalIndent(a.tasks, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(a.tasksPath(), append(contents, '\n'), 0600)
}

func (a *App) recordTask(id, status string, enabled bool, txHash, message string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.tasks {
		if a.tasks[index].ID == id {
			a.tasks[index].Status = status
			a.tasks[index].Enabled = enabled
			a.tasks[index].LastTxHash = txHash
			a.tasks[index].LastError = message
			a.tasks[index].LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			_ = a.saveLocked()
			return
		}
	}
}

func (a *App) setTaskRuntimeStatus(id, status string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.tasks {
		if a.tasks[index].ID == id && a.tasks[index].Enabled {
			a.tasks[index].Status = status
			a.tasks[index].LastError = ""
			_ = a.saveLocked()
			return
		}
	}
}

func isRuntimeTaskStatus(status string) bool {
	return status == "scheduled" || status == "watching"
}

func (a *App) resetRuntimeTaskStatusesLocked() bool {
	changed := false
	for index := range a.tasks {
		if a.tasks[index].Enabled && isRuntimeTaskStatus(a.tasks[index].Status) {
			a.tasks[index].Status = "ready"
			changed = true
		}
	}
	return changed
}

func (a *App) checkedTask(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.tasks {
		if a.tasks[index].ID == id {
			a.tasks[index].LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
			return
		}
	}
}

func (a *App) wallets() []string {
	accounts := a.keystore().Accounts()
	addresses := make([]string, 0, len(accounts))
	for _, account := range accounts {
		addresses = append(addresses, account.Address.Hex())
	}
	return addresses
}

func (a *App) hasWallet(address common.Address) bool {
	_, ok := findAppWallet(a.keystore(), address)
	return ok
}

func (a *App) keystore() *keystore.KeyStore {
	return a.ks
}

func (a *App) tasksPath() string { return filepath.Join(a.dataDir, "tasks.json") }

func findAppWallet(ks *keystore.KeyStore, address common.Address) (accounts.Account, bool) {
	for _, account := range ks.Accounts() {
		if account.Address == address {
			return account, true
		}
	}
	return accounts.Account{}, false
}

func validateAppTask(task MintTask) error {
	if task.Name == "" || !common.IsHexAddress(task.WalletAddress) || !common.IsHexAddress(task.Contract) {
		return errors.New("name, wallet, and contract are required")
	}
	if task.Network.Name == "" || task.Network.ChainID <= 0 || task.Network.RPCURL == "" {
		return errors.New("network name, chain ID, and RPC URL are required")
	}
	if !strings.HasSuffix(task.SaleGate.Function, "()") || strings.Contains(task.SaleGate.Function, " ") || taskPollInterval(task.SaleGate) < 100*time.Millisecond {
		return errors.New("sale gate must be a no-argument boolean function, checked every 100 milliseconds or longer")
	}
	value, ok := new(big.Int).SetString(task.Mint.ValueWei, 10)
	if !ok || value.Sign() < 0 {
		return errors.New("mint value must be a non-negative wei amount")
	}
	if task.Mint.RawCalldata != "" {
		if _, err := appParseHex(task.Mint.RawCalldata); err != nil {
			return errors.New("raw calldata must be valid hex")
		}
	} else if !appQuantityMethod.MatchString(task.Mint.Method) || task.Mint.Quantity == 0 {
		return errors.New("use a quantity-only method such as mint(uint256), or enter verified raw calldata")
	}
	if _, err := appPositiveWei(task.Mint.MaxGasPriceWei, "max gas price"); err != nil {
		return err
	}
	if _, err := appPositiveWei(task.Mint.MaxTotalCostWei, "max total cost"); err != nil {
		return err
	}
	return nil
}

func taskPollInterval(gate SaleGate) time.Duration {
	if gate.PollIntervalMilliseconds > 0 {
		return time.Duration(gate.PollIntervalMilliseconds) * time.Millisecond
	}
	return time.Duration(gate.PollIntervalSeconds) * time.Second
}

type appTransactionFeeQuote struct {
	dynamic  bool
	gasPrice *big.Int
	feeCap   *big.Int
	tipCap   *big.Int
}

func (quote appTransactionFeeQuote) maxFeePerGas() *big.Int {
	if quote.dynamic {
		return quote.feeCap
	}
	return quote.gasPrice
}

func appFeeQuote(ctx context.Context, client *ethclient.Client, endpoint string, header *types.Header) (appTransactionFeeQuote, error) {
	if header != nil && header.BaseFee != nil {
		tip, err := rpcRead(ctx, endpoint, func() (*big.Int, error) {
			return client.SuggestGasTipCap(ctx)
		})
		if err != nil {
			return appTransactionFeeQuote{}, fmt.Errorf("suggest priority fee: %w", err)
		}
		if tip.Sign() < 0 {
			return appTransactionFeeQuote{}, errors.New("RPC returned a negative priority fee")
		}
		// Two blocks of base-fee headroom prevents a normal base-fee increase
		// between simulation and broadcast from invalidating the transaction.
		return appDynamicFeeQuote(header.BaseFee, tip), nil
	}
	gasPrice, err := rpcRead(ctx, endpoint, func() (*big.Int, error) {
		return client.SuggestGasPrice(ctx)
	})
	if err != nil {
		return appTransactionFeeQuote{}, fmt.Errorf("suggest gas price: %w", err)
	}
	return appTransactionFeeQuote{gasPrice: gasPrice}, nil
}

func appDynamicFeeQuote(baseFee, tip *big.Int) appTransactionFeeQuote {
	feeCap := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)
	return appTransactionFeeQuote{dynamic: true, feeCap: feeCap, tipCap: new(big.Int).Set(tip)}
}

func appSaleState(ctx context.Context, client *ethclient.Client, endpoint string, contract common.Address, signature string) (bool, error) {
	result, err := rpcRead(ctx, endpoint, func() ([]byte, error) {
		return client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: crypto.Keccak256([]byte(signature))[:4]}, nil)
	})
	if err != nil {
		return false, err
	}
	if len(result) != 32 || result[31] > 1 {
		return false, errors.New("sale gate did not return a bool")
	}
	for _, byteValue := range result[:31] {
		if byteValue != 0 {
			return false, errors.New("sale gate did not return a bool")
		}
	}
	return result[31] == 1, nil
}

func appMintData(call MintConfig) ([]byte, error) {
	if call.RawCalldata != "" {
		return appParseHex(call.RawCalldata)
	}
	matches := appQuantityMethod.FindStringSubmatch(call.Method)
	if len(matches) != 2 || call.Quantity == 0 {
		return nil, errors.New("unsupported mint call")
	}
	definition, _ := json.Marshal([]map[string]any{{
		"type": "function", "name": matches[1], "stateMutability": "payable",
		"inputs": []map[string]string{{"name": "quantity", "type": "uint256"}}, "outputs": []any{},
	}})
	contractABI, err := abi.JSON(strings.NewReader(string(definition)))
	if err != nil {
		return nil, err
	}
	return contractABI.Pack(matches[1], new(big.Int).SetUint64(call.Quantity))
}

func appParseHex(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if len(value)%2 != 0 {
		return nil, errors.New("invalid hex")
	}
	return hex.DecodeString(value)
}

func appPositiveWei(value, name string) (*big.Int, error) {
	amount, ok := new(big.Int).SetString(value, 10)
	if !ok || amount.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be a positive wei amount", name)
	}
	return amount, nil
}

func newTaskID() (string, error) {
	value := make([]byte, 12)
	if _, err := crand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

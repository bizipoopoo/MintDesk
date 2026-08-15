package main

import (
	"context"
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

type ChainConfig struct {
	Name    string `json:"name"`
	ChainID int64  `json:"chainId"`
	RPCURL  string `json:"rpcUrl"`
}

type SaleGate struct {
	Function            string `json:"function"`
	Expect              bool   `json:"expect"`
	PollIntervalSeconds int    `json:"pollIntervalSeconds"`
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
	if password == "" {
		return "", errors.New("a local keystore password is required")
	}
	key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(privateKey), "0x"))
	if err != nil {
		return "", errors.New("private key is invalid")
	}
	account, err := a.keystore().ImportECDSA(key, password)
	if err != nil {
		return "", fmt.Errorf("import encrypted wallet: %w", err)
	}
	return account.Address.Hex(), nil
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
	seed := bip39.NewSeed(normalized, "")
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
	result := make([]string, 0, count)
	for index := 0; index < count; index++ {
		child, err := key.NewChildKey(uint32(index))
		if err != nil {
			return nil, err
		}
		privateKey, err := crypto.ToECDSA(child.Key)
		if err != nil {
			return nil, err
		}
		account, err := a.keystore().ImportECDSA(privateKey, password)
		if err != nil {
			if strings.Contains(err.Error(), "already exists") {
				result = append(result, crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
				continue
			}
			return nil, fmt.Errorf("import derived address %d: %w", index, err)
		}
		result = append(result, account.Address.Hex())
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
		delay, stageEnd, err := openSeaWatchDelay(task.OpenSea.Stage, time.Now())
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
			a.recordTask(task.ID, "failed", false, "", "OpenSea 所选 mint 阶段已结束")
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
	chainID, err := client.ChainID(ctx)
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
	interval := time.Duration(task.SaleGate.PollIntervalSeconds) * time.Second
	for {
		active, err := appSaleState(ctx, client, contract, task.SaleGate.Function)
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
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return "", err
	}
	if gasPrice.Cmp(maxGasPrice) > 0 {
		return "", fmt.Errorf("suggested gas price %s exceeds the task cap %s", gasPrice, maxGasPrice)
	}
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: account.Address, To: &to, Value: value, Data: data, GasPrice: gasPrice})
	if err != nil {
		return "", fmt.Errorf("mint simulation failed: %w", err)
	}
	totalCost := new(big.Int).Add(value, new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasLimit)))
	if totalCost.Cmp(maxTotalCost) > 0 {
		return "", fmt.Errorf("estimated total cost %s exceeds the task cap %s", totalCost, maxTotalCost)
	}

	pendingNonce, err := client.PendingNonceAt(ctx, account.Address)
	if err != nil {
		return "", err
	}
	if state.next == nil || pendingNonce > *state.next {
		state.next = &pendingNonce
	}
	nonce := *state.next
	tx := types.NewTransaction(nonce, to, value, gasLimit, gasPrice, data)
	signed, err := a.keystore().SignTx(account, tx, chainID)
	if err != nil {
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
	if !strings.HasSuffix(task.SaleGate.Function, "()") || strings.Contains(task.SaleGate.Function, " ") || task.SaleGate.PollIntervalSeconds < 2 {
		return errors.New("sale gate must be a no-argument boolean function, checked every 2 seconds or longer")
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

func appSaleState(ctx context.Context, client *ethclient.Client, contract common.Address, signature string) (bool, error) {
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &contract, Data: crypto.Keccak256([]byte(signature))[:4]}, nil)
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

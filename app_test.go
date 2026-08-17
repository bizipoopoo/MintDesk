package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/tyler-smith/go-bip39"
)

func TestQuantityMintCalldata(t *testing.T) {
	data, err := appMintData(MintConfig{Method: "mint(uint256)", Quantity: 100})
	if err != nil {
		t.Fatal(err)
	}
	wantSelector := crypto.Keccak256([]byte("mint(uint256)"))[:4]
	if len(data) != 36 || !bytes.Equal(data[:4], wantSelector) || data[35] != 100 {
		t.Fatalf("unexpected calldata: %x", data)
	}
}

func TestAssetResponsesDisableWebViewCache(t *testing.T) {
	handler := noCacheAssetMiddleware(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "https://wails.local/index.html", nil))
	if recorder.Header().Get("Cache-Control") != "no-store, no-cache, must-revalidate, max-age=0" {
		t.Fatalf("unexpected cache control: %q", recorder.Header().Get("Cache-Control"))
	}
	if recorder.Header().Get("Pragma") != "no-cache" || recorder.Header().Get("Expires") != "0" {
		t.Fatalf("missing legacy no-cache headers: %#v", recorder.Header())
	}
}

func TestNonceCoordinatorSharesWalletChainState(t *testing.T) {
	coordinator := nonceCoordinator{states: make(map[string]*nonceState)}
	address := common.HexToAddress("0x0000000000000000000000000000000000000001")
	first := coordinator.state(8453, address)
	second := coordinator.state(8453, address)
	otherChain := coordinator.state(1, address)
	if first != second {
		t.Fatal("expected same wallet and chain to share nonce state")
	}
	if first == otherChain {
		t.Fatal("expected different chains to use separate nonce state")
	}
}

func TestDynamicFeeQuoteIncludesBaseFeeHeadroom(t *testing.T) {
	quote := appDynamicFeeQuote(big.NewInt(21_710_000), big.NewInt(0))
	if !quote.dynamic || quote.tipCap.Sign() != 0 || quote.feeCap.Cmp(big.NewInt(43_420_000)) != 0 {
		t.Fatalf("unexpected dynamic fee quote: %#v", quote)
	}
}

func TestRetryableRPCErrorClassification(t *testing.T) {
	for _, message := range []string{
		"429 Too Many Requests",
		"502 Bad Gateway",
		"context deadline exceeded",
		"max fee per gas less than block base fee",
	} {
		if !isRetryableRPCError(errors.New(message)) {
			t.Fatalf("expected retryable error: %s", message)
		}
	}
	if isRetryableRPCError(errors.New("mint simulation failed with 50000441 gas: execution reverted")) {
		t.Fatal("gas amount must not be mistaken for an HTTP 5xx error")
	}
}

func TestTaskPollIntervalSupportsFourSpeedsAndLegacySeconds(t *testing.T) {
	cases := []struct {
		name string
		gate SaleGate
		want time.Duration
	}{
		{name: "extreme", gate: SaleGate{PollIntervalMilliseconds: 100}, want: 100 * time.Millisecond},
		{name: "fast", gate: SaleGate{PollIntervalMilliseconds: 500}, want: 500 * time.Millisecond},
		{name: "slow", gate: SaleGate{PollIntervalMilliseconds: 2000}, want: 2 * time.Second},
		{name: "very slow", gate: SaleGate{PollIntervalMilliseconds: 5000}, want: 5 * time.Second},
		{name: "legacy seconds", gate: SaleGate{PollIntervalSeconds: 5}, want: 5 * time.Second},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := taskPollInterval(test.gate); got != test.want {
				t.Fatalf("unexpected interval: got %s want %s", got, test.want)
			}
		})
	}
	for _, milliseconds := range []int{100, 500, 2000, 5000} {
		if !isSupportedPollingSpeed(milliseconds) {
			t.Fatalf("expected %dms to be supported", milliseconds)
		}
	}
	if isSupportedPollingSpeed(1000) {
		t.Fatal("unexpected custom polling speed")
	}
}

func TestResetRuntimeTaskStatuses(t *testing.T) {
	app := &App{tasks: []MintTask{
		{ID: "scheduled", Enabled: true, Status: "scheduled"},
		{ID: "watching", Enabled: true, Status: "watching"},
		{ID: "completed", Enabled: false, Status: "broadcast"},
	}}
	if !app.resetRuntimeTaskStatusesLocked() {
		t.Fatal("expected runtime statuses to be reset")
	}
	if app.tasks[0].Status != "ready" || app.tasks[1].Status != "ready" {
		t.Fatalf("runtime tasks were not reset: %#v", app.tasks)
	}
	if app.tasks[2].Status != "broadcast" {
		t.Fatalf("completed task status must be preserved: %#v", app.tasks[2])
	}
}

func TestCollectionTaskManagement(t *testing.T) {
	contract := "0x0000000000000000000000000000000000000001"
	otherContract := "0x0000000000000000000000000000000000000002"
	app := &App{dataDir: t.TempDir(), tasks: []MintTask{
		{ID: "first", Contract: contract, Network: ChainConfig{ChainID: 4663}, Enabled: true, Status: "ready"},
		{ID: "second", Contract: common.HexToAddress(contract).Hex(), Network: ChainConfig{ChainID: 4663}, Enabled: true, Status: "watching"},
		{ID: "other", Contract: otherContract, Network: ChainConfig{ChainID: 4663}, Enabled: true, Status: "ready"},
	}}
	if err := app.SetCollectionTasksEnabled(4663, contract, false); err != nil {
		t.Fatal(err)
	}
	if app.tasks[0].Enabled || app.tasks[1].Enabled || !app.tasks[2].Enabled {
		t.Fatalf("unexpected collection enable state: %#v", app.tasks)
	}
	if err := app.SetCollectionTasksEnabled(4663, contract, true); err != nil {
		t.Fatal(err)
	}
	if !app.tasks[0].Enabled || !app.tasks[1].Enabled || app.tasks[1].Status != "ready" {
		t.Fatalf("collection tasks were not reset when enabled: %#v", app.tasks)
	}
	deleted, err := app.DeleteCollectionTasks(4663, contract)
	if err != nil || deleted != 2 {
		t.Fatalf("unexpected delete result: deleted=%d err=%v", deleted, err)
	}
	if len(app.tasks) != 1 || app.tasks[0].ID != "other" {
		t.Fatalf("unexpected tasks after collection delete: %#v", app.tasks)
	}
}

func TestCollectionChangesBlockedWhileRunnerActive(t *testing.T) {
	contract := "0x0000000000000000000000000000000000000001"
	app := &App{dataDir: t.TempDir(), running: true, tasks: []MintTask{{ID: "active", Contract: contract, Network: ChainConfig{ChainID: 4663}, Enabled: true}}}
	if err := app.SetCollectionTasksEnabled(4663, contract, false); err == nil {
		t.Fatal("expected collection disable to be blocked while runner is active")
	}
	if _, err := app.DeleteCollectionTasks(4663, contract); err == nil {
		t.Fatal("expected collection delete to be blocked while runner is active")
	}
}

func TestBatchWalletImportAndMnemonicGeneration(t *testing.T) {
	store := keystore.NewKeyStore(filepath.Join(t.TempDir(), "keystore"), keystore.LightScryptN, keystore.LightScryptP)
	app := &App{ks: store}
	keys := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		key, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, hex.EncodeToString(crypto.FromECDSA(key)))
	}
	addresses, err := app.ImportPrivateKeys(keys, "test-password")
	if err != nil || len(addresses) != 2 {
		t.Fatalf("unexpected batch import: addresses=%v err=%v", addresses, err)
	}
	generated, err := app.GenerateMnemonicWallets("test-password", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bip39.IsMnemonicValid(generated.Mnemonic) || len(generated.Addresses) != 3 || len(store.Accounts()) != 5 {
		t.Fatalf("unexpected generated wallet batch: %#v accounts=%d", generated, len(store.Accounts()))
	}
}

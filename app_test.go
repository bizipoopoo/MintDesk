package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
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

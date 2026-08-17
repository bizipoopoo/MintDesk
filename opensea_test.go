package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func TestOpenSeaStrategyWatchDelayAndLegacyStage(t *testing.T) {
	start := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	public := OpenSeaStage{StageType: "PUBLIC_SALE", StageIndex: 0, StartTime: start.Add(time.Hour).Format(time.RFC3339Nano), EndTime: start.Add(2 * time.Hour).Format(time.RFC3339Nano)}
	wl := OpenSeaStage{StageType: "SIGNED_PRESALE", StageIndex: 2, StartTime: start.Format(time.RFC3339Nano), EndTime: start.Add(time.Hour).Format(time.RFC3339Nano)}
	snapshot := &OpenSeaTaskSnapshot{Stages: []OpenSeaStage{public, wl}}
	delay, end, err := openSeaStrategyWatchDelay(snapshot, start.Add(-10*time.Minute))
	if err != nil || delay != 9*time.Minute || !end.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("unexpected strategy schedule: delay=%s end=%s err=%v", delay, end, err)
	}
	ordered := openSeaTaskStages(snapshot)
	if len(ordered) != 2 || ordered[0].StageIndex != 2 || ordered[1].StageIndex != 0 {
		t.Fatalf("stages were not sorted chronologically: %#v", ordered)
	}
	legacy := openSeaTaskStages(&OpenSeaTaskSnapshot{Stage: public})
	if len(legacy) != 1 || legacy[0].StageType != "PUBLIC_SALE" {
		t.Fatalf("legacy stage was not preserved: %#v", legacy)
	}
}

func TestOpenSeaWatchDelay(t *testing.T) {
	start := time.Date(2026, 8, 19, 1, 0, 0, 0, time.UTC)
	stage := OpenSeaStage{
		StartTime: start.Format(time.RFC3339Nano),
		EndTime:   start.Add(time.Hour).Format(time.RFC3339Nano),
	}

	delay, end, err := openSeaWatchDelay(stage, start.Add(-10*time.Minute))
	if err != nil || delay != 9*time.Minute || !end.Equal(start.Add(time.Hour)) {
		t.Fatalf("unexpected distant schedule: delay=%s end=%s err=%v", delay, end, err)
	}

	delay, _, err = openSeaWatchDelay(stage, start.Add(-30*time.Second))
	if err != nil || delay != 0 {
		t.Fatalf("task inside the one-minute watch window must start immediately: delay=%s err=%v", delay, err)
	}

	delay, _, err = openSeaWatchDelay(stage, start.Add(10*time.Minute))
	if err != nil || delay != 0 {
		t.Fatalf("active mint stage must start watching immediately: delay=%s err=%v", delay, err)
	}

	if _, _, err = openSeaWatchDelay(stage, start.Add(time.Hour)); err == nil {
		t.Fatal("ended mint stage must not start a watcher")
	}
}

func TestParseOpenSeaRobinhoodDrop(t *testing.T) {
	html := []byte(`<script>(window.data={"collectionBySlug":{"slug":"demo-drop","name":"Demo Drop","address":"0x12a4c7659a4b7c4a2870b5167c4f8b014c7fa690","isVerified":true,"isApproved":true,"chain":{"identifier":"robinhood"},"dropBySlug":{"maxSupply":2222,"totalSupply":12,"stages":[{"label":"Public","stageType":"PUBLIC_SALE","stageIndex":0,"startTime":"2026-08-18T21:00:00.000Z","endTime":"2026-08-18T22:00:00.000Z","maxTotalMintableByWallet":5,"price":{"token":{"unit":0.125,"symbol":"ETH","chain":{"identifier":"robinhood"}}}}]}}});</script>`)
	drop, err := parseOpenSeaHTML("https://opensea.io/collection/demo-drop", "demo-drop", html)
	if err != nil {
		t.Fatal(err)
	}
	if drop.Chain != "robinhood" || drop.Contract != common.HexToAddress("0x12a4c7659a4b7c4a2870b5167c4f8b014c7fa690").Hex() {
		t.Fatalf("unexpected parsed collection: %#v", drop)
	}
	if len(drop.Stages) != 1 || !drop.Stages[0].AutoMintSupported || drop.Stages[0].PriceWei != "125000000000000000" {
		t.Fatalf("unexpected parsed stage: %#v", drop.Stages)
	}
	if drop.RiskFlags == nil || len(drop.RiskFlags) != 0 {
		t.Fatalf("expected a non-nil empty risk flag list, got %#v", drop.RiskFlags)
	}
	encoded, err := json.Marshal(drop)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"riskFlags":[]`)) {
		t.Fatalf("risk flags must serialize as an array: %s", encoded)
	}
}

func TestParseOpenSeaSupportsAllSeaDropStages(t *testing.T) {
	html := []byte(`<script>{"collectionBySlug":{"slug":"all-stages","name":"All Stages","address":"0x12a4c7659a4b7c4a2870b5167c4f8b014c7fa690","isVerified":true,"isApproved":true,"chain":{"identifier":"robinhood"},"dropBySlug":{"maxSupply":100,"totalSupply":0,"stages":[{"label":"Signed","stageType":"SIGNED_PRESALE","stageIndex":1,"startTime":"2026-08-18T20:00:00Z","endTime":"2026-08-18T21:00:00Z","maxTotalMintableByWallet":2,"price":{"token":{"unit":0.001,"symbol":"ETH","contractAddress":"0x0000000000000000000000000000000000000000","chain":{"identifier":"robinhood"}}}},{"label":"Merkle","stageType":"MERKLE_PRESALE","stageIndex":2,"startTime":"2026-08-18T21:00:00Z","endTime":"2026-08-18T22:00:00Z","maxTotalMintableByWallet":3,"price":{"token":{"unit":0.001,"symbol":"ETH","contractAddress":"0x0000000000000000000000000000000000000000","chain":{"identifier":"robinhood"}}}},{"label":"Public","stageType":"PUBLIC_SALE","stageIndex":0,"startTime":"2026-08-18T22:00:00Z","endTime":"2026-08-18T23:00:00Z","maxTotalMintableByWallet":5,"price":{"token":{"unit":0.002,"symbol":"ETH","contractAddress":"0x0000000000000000000000000000000000000000","chain":{"identifier":"robinhood"}}}}]}}}</script>`)
	drop, err := parseOpenSeaHTML("https://opensea.io/collection/all-stages", "all-stages", html)
	if err != nil {
		t.Fatal(err)
	}
	if len(drop.Stages) != 3 {
		t.Fatalf("unexpected stage count: %#v", drop.Stages)
	}
	for _, stage := range drop.Stages {
		if !stage.AutoMintSupported || !supportedOpenSeaStage(stage) {
			t.Fatalf("stage should be supported: %#v", stage)
		}
	}
}

func TestSelectOpenSeaMintQuoteFindsWalletAllocation(t *testing.T) {
	wallet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	nft := common.HexToAddress("0x2000000000000000000000000000000000000002")
	calls := 0
	requester := func(_ context.Context, gotWallet, gotNFT common.Address, quantity uint64) (openSeaMintTransaction, error) {
		calls++
		if gotWallet != wallet || gotNFT != nft {
			t.Fatal("requester received the wrong addresses")
		}
		if quantity > 2 {
			return openSeaMintTransaction{}, &openSeaMintQuoteError{Codes: []string{"MaxQuantityExceededError"}}
		}
		return openSeaMintTransaction{Value: fmt.Sprintf("%d", quantity)}, nil
	}
	quote, quantity, err := selectOpenSeaMintQuote(context.Background(), requester, wallet, nft, 5)
	if err != nil || quantity != 2 || quote.Value != "2" {
		t.Fatalf("unexpected selected quote: quote=%#v quantity=%d err=%v", quote, quantity, err)
	}
	if calls > 5 {
		t.Fatalf("allocation probing should be logarithmic, got %d calls", calls)
	}

	_, quantity, err = selectOpenSeaMintQuote(context.Background(), func(context.Context, common.Address, common.Address, uint64) (openSeaMintTransaction, error) {
		return openSeaMintTransaction{}, &openSeaMintQuoteError{Codes: []string{"NotEligibleError"}}
	}, wallet, nft, 5)
	var quoteErr *openSeaMintQuoteError
	if quantity != 0 || !errors.As(err, &quoteErr) {
		t.Fatalf("ineligible wallet must remain eligible for public fallback: quantity=%d err=%v", quantity, err)
	}
}

func TestParseAndValidateOpenSeaSignedQuote(t *testing.T) {
	start := time.Date(2026, 8, 18, 14, 0, 0, 0, time.UTC)
	stage := OpenSeaStage{
		StageType: "SIGNED_PRESALE", StageIndex: 1,
		StartTime: start.Format(time.RFC3339Nano), EndTime: start.Add(time.Hour).Format(time.RFC3339Nano),
		PriceWei: "11", MaxTotalMintableByWallet: 2, AutoMintSupported: true,
	}
	wallet := common.HexToAddress("0x1000000000000000000000000000000000000001")
	nft := common.HexToAddress("0x2000000000000000000000000000000000000002")
	seaDrop := common.HexToAddress("0x00005EA00Ac477B1030CE78506496e8C2dE24bf5")
	fee := common.HexToAddress("0x3000000000000000000000000000000000000003")
	params := seaDropMintParams{
		MintPrice: big.NewInt(11), MaxTotalMintableByWallet: big.NewInt(2),
		StartTime: big.NewInt(start.Unix()), EndTime: big.NewInt(start.Add(time.Hour).Unix()),
		DropStageIndex: big.NewInt(1), MaxTokenSupplyForStage: big.NewInt(100), FeeBps: big.NewInt(250),
	}
	calldata, err := seaDropABI.Pack("mintSigned", nft, fee, wallet, big.NewInt(2), params, big.NewInt(9), []byte{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf(`{"data":{"swap":{"actions":[{"__typename":"TransactionAction","transactionSubmissionData":{"to":"%s","data":"0x%x","value":"22","chain":{"identifier":"robinhood","networkId":4663}}}],"errors":[]}}}`, seaDrop.Hex(), calldata))
	quote, err := parseOpenSeaMintQuote(body)
	if err != nil {
		t.Fatal(err)
	}
	data, value, err := validateOpenSeaAllowlistQuote(quote, stage, wallet, nft, seaDrop, 2, 0, []common.Address{fee})
	if err != nil || !bytes.Equal(data, calldata) || value.Cmp(big.NewInt(22)) != 0 {
		t.Fatalf("signed quote validation failed: value=%v err=%v", value, err)
	}
	quote.To = nft.Hex()
	if _, _, err := validateOpenSeaAllowlistQuote(quote, stage, wallet, nft, seaDrop, 2, 0, []common.Address{fee}); err == nil {
		t.Fatal("a quote targeting the NFT contract instead of SeaDrop must be rejected")
	}
}

func TestNormalizeOpenSeaURL(t *testing.T) {
	canonical, slug, err := normalizeOpenSeaURL("https://opensea.io/collection/demo-drop/overview?tab=mint")
	if err != nil || canonical != "https://opensea.io/collection/demo-drop" || slug != "demo-drop" {
		t.Fatalf("unexpected normalization: %q %q %v", canonical, slug, err)
	}
	if _, _, err := normalizeOpenSeaURL("https://opensea.io.evil.example/collection/demo-drop"); err == nil {
		t.Fatal("expected lookalike host to be rejected")
	}
}

func TestInspectOpenSeaLive(t *testing.T) {
	if os.Getenv("MINTDESK_LIVE_OPENSEA_TEST") == "" {
		t.Skip("set MINTDESK_LIVE_OPENSEA_TEST=1 to query OpenSea")
	}
	var drop OpenSeaDrop
	for _, collectionURL := range []string{
		"https://opensea.io/collection/wif-outlaws",
		"https://opensea.io/collection/chainraiders/overview",
	} {
		parsed, err := inspectOpenSeaDrop(context.Background(), collectionURL)
		if err != nil {
			t.Fatalf("inspect %s: %v", collectionURL, err)
		}
		if parsed.Chain != "robinhood" || len(parsed.Stages) == 0 || parsed.RiskFlags == nil {
			t.Fatalf("unexpected live drop for %s: %#v", collectionURL, parsed)
		}
		drop = parsed
	}
	client, err := ethclient.Dial(defaultRobinhoodRPC)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	seaDrop, err := findSeaDrop(context.Background(), client, defaultRobinhoodRPC, common.HexToAddress(drop.Contract))
	if err != nil {
		t.Fatal(err)
	}
	config, err := readPublicDrop(context.Background(), client, defaultRobinhoodRPC, seaDrop, common.HexToAddress(drop.Contract))
	if err != nil || config.MintPrice == nil {
		t.Fatalf("cannot read public drop: %#v %v", config, err)
	}
	recipients, err := readAllowedFeeRecipients(context.Background(), client, defaultRobinhoodRPC, seaDrop, common.HexToAddress(drop.Contract))
	if err != nil || len(recipients) == 0 {
		t.Fatalf("cannot read fee recipients: %#v %v", recipients, err)
	}
}

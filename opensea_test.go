package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

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
	seaDrop, err := findSeaDrop(context.Background(), client, common.HexToAddress(drop.Contract))
	if err != nil {
		t.Fatal(err)
	}
	config, err := readPublicDrop(context.Background(), client, seaDrop, common.HexToAddress(drop.Contract))
	if err != nil || config.MintPrice == nil {
		t.Fatalf("cannot read public drop: %#v %v", config, err)
	}
	recipients, err := readAllowedFeeRecipients(context.Background(), client, seaDrop, common.HexToAddress(drop.Contract))
	if err != nil || len(recipients) == 0 {
		t.Fatalf("cannot read fee recipients: %#v %v", recipients, err)
	}
}

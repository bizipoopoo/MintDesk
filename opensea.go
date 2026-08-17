package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	robinhoodChainID        int64 = 4663
	defaultRobinhoodRPC           = "https://rpc.mainnet.chain.robinhood.com"
	openSeaMaxPageBytes           = 12 << 20
	openSeaWatchLeadTime          = time.Minute
	openSeaQuoteMinInterval       = 250 * time.Millisecond
)

var openSeaGraphQLEndpoint = "https://gql.opensea.io/graphql"

type OpenSeaStage struct {
	Label                    string `json:"label"`
	StageType                string `json:"stageType"`
	StageIndex               int    `json:"stageIndex"`
	StartTime                string `json:"startTime"`
	EndTime                  string `json:"endTime"`
	MaxTotalMintableByWallet uint64 `json:"maxTotalMintableByWallet"`
	Price                    string `json:"price"`
	PriceWei                 string `json:"priceWei"`
	PriceSymbol              string `json:"priceSymbol"`
	PriceContract            string `json:"priceContract"`
	AutoMintSupported        bool   `json:"autoMintSupported"`
	BlockReason              string `json:"blockReason,omitempty"`
}

type OpenSeaDrop struct {
	URL             string         `json:"url"`
	Slug            string         `json:"slug"`
	Name            string         `json:"name"`
	Chain           string         `json:"chain"`
	Contract        string         `json:"contract"`
	Verified        bool           `json:"verified"`
	Approved        bool           `json:"approved"`
	ExternalURL     string         `json:"externalUrl"`
	TwitterUsername string         `json:"twitterUsername"`
	MaxSupply       uint64         `json:"maxSupply"`
	TotalSupply     uint64         `json:"totalSupply"`
	Stages          []OpenSeaStage `json:"stages"`
	RiskFlags       []string       `json:"riskFlags"`
}

type OpenSeaTaskInput struct {
	Name                     string   `json:"name"`
	OpenSeaURL               string   `json:"openSeaUrl"`
	WalletAddresses          []string `json:"walletAddresses"`
	QuantityPerWallet        uint64   `json:"quantityPerWallet"`
	RPCURL                   string   `json:"rpcUrl"`
	PollIntervalSeconds      int      `json:"pollIntervalSeconds,omitempty"`
	PollIntervalMilliseconds int      `json:"pollIntervalMilliseconds,omitempty"`
	MaxGasPriceGwei          string   `json:"maxGasPriceGwei"`
	MaxTotalCostETH          string   `json:"maxTotalCostEth"`
}

type OpenSeaTaskSnapshot struct {
	Slug           string         `json:"slug"`
	Stage          OpenSeaStage   `json:"stage,omitempty"` // Legacy single-stage tasks.
	Stages         []OpenSeaStage `json:"stages,omitempty"`
	TargetQuantity uint64         `json:"targetQuantity,omitempty"`
	SpentWei       string         `json:"spentWei,omitempty"`
	Collection     string         `json:"collection"`
}

func supportedOpenSeaStage(stage OpenSeaStage) bool {
	switch stage.StageType {
	case "PUBLIC_SALE", "SIGNED_PRESALE", "MERKLE_PRESALE":
		return stage.AutoMintSupported
	default:
		return false
	}
}

func openSeaTaskStages(snapshot *OpenSeaTaskSnapshot) []OpenSeaStage {
	if snapshot == nil {
		return nil
	}
	stages := append([]OpenSeaStage(nil), snapshot.Stages...)
	if len(stages) == 0 && snapshot.Stage.StageType != "" {
		stages = append(stages, snapshot.Stage)
	}
	sort.SliceStable(stages, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339Nano, stages[i].StartTime)
		right, rightErr := time.Parse(time.RFC3339Nano, stages[j].StartTime)
		if leftErr != nil || rightErr != nil || left.Equal(right) {
			return stages[i].StageIndex < stages[j].StageIndex
		}
		return left.Before(right)
	})
	return stages
}

func openSeaTaskTarget(task MintTask) uint64 {
	if task.OpenSea != nil && task.OpenSea.TargetQuantity > 0 {
		return task.OpenSea.TargetQuantity
	}
	return task.Mint.Quantity
}

func openSeaTaskSpent(snapshot *OpenSeaTaskSnapshot) *big.Int {
	if snapshot == nil {
		return new(big.Int)
	}
	spent, ok := new(big.Int).SetString(snapshot.SpentWei, 10)
	if !ok || spent.Sign() < 0 {
		return new(big.Int)
	}
	return spent
}

func openSeaStrategyWatchDelay(snapshot *OpenSeaTaskSnapshot, now time.Time) (time.Duration, time.Time, error) {
	stages := openSeaTaskStages(snapshot)
	var earliest time.Time
	var latestEnd time.Time
	for _, stage := range stages {
		start, startErr := time.Parse(time.RFC3339Nano, stage.StartTime)
		end, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
		if startErr != nil || endErr != nil || !end.After(start) {
			return 0, time.Time{}, errors.New("an OpenSea stage time is invalid; recreate the task")
		}
		if end.After(latestEnd) {
			latestEnd = end
		}
		if now.Before(end) && (earliest.IsZero() || start.Before(earliest)) {
			earliest = start
		}
	}
	if earliest.IsZero() || latestEnd.IsZero() || !now.Before(latestEnd) {
		return 0, latestEnd, errors.New("all selected OpenSea mint stages have ended")
	}
	watchAt := earliest.Add(-openSeaWatchLeadTime)
	if now.Before(watchAt) {
		return watchAt.Sub(now), latestEnd, nil
	}
	return 0, latestEnd, nil
}

func openSeaWatchDelay(stage OpenSeaStage, now time.Time) (time.Duration, time.Time, error) {
	start, startErr := time.Parse(time.RFC3339Nano, stage.StartTime)
	end, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
	if startErr != nil || endErr != nil || !end.After(start) {
		return 0, time.Time{}, errors.New("the OpenSea stage time is invalid; recreate the task")
	}
	if !now.Before(end) {
		return 0, end, errors.New("the selected OpenSea mint stage has ended")
	}
	watchAt := start.Add(-openSeaWatchLeadTime)
	if now.Before(watchAt) {
		return watchAt.Sub(now), end, nil
	}
	return 0, end, nil
}

type openSeaCollectionPayload struct {
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	Address         string `json:"address"`
	IsVerified      bool   `json:"isVerified"`
	IsApproved      bool   `json:"isApproved"`
	ExternalURL     string `json:"externalUrl"`
	TwitterUsername string `json:"twitterUsername"`
	Chain           struct {
		Identifier string `json:"identifier"`
	} `json:"chain"`
	DropBySlug *openSeaDropPayload `json:"dropBySlug"`
}

type openSeaDropPayload struct {
	MaxSupply   uint64 `json:"maxSupply"`
	TotalSupply uint64 `json:"totalSupply"`
	Stages      []struct {
		Label                    string `json:"label"`
		StageType                string `json:"stageType"`
		StageIndex               int    `json:"stageIndex"`
		StartTime                string `json:"startTime"`
		EndTime                  string `json:"endTime"`
		MaxTotalMintableByWallet uint64 `json:"maxTotalMintableByWallet"`
		Price                    struct {
			Token struct {
				Unit            json.Number `json:"unit"`
				Symbol          string      `json:"symbol"`
				ContractAddress string      `json:"contractAddress"`
				Chain           struct {
					Identifier string `json:"identifier"`
				} `json:"chain"`
			} `json:"token"`
		} `json:"price"`
	} `json:"stages"`
}

func (a *App) InspectOpenSeaDrop(rawURL string) (OpenSeaDrop, error) {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return inspectOpenSeaDrop(ctx, rawURL)
}

func inspectOpenSeaDrop(ctx context.Context, rawURL string) (OpenSeaDrop, error) {
	canonical, slug, err := normalizeOpenSeaURL(rawURL)
	if err != nil {
		return OpenSeaDrop{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, canonical, nil)
	if err != nil {
		return OpenSeaDrop{}, err
	}
	req.Header.Set("User-Agent", "MintDesk/1.0 (+local OpenSea drop inspector)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return OpenSeaDrop{}, fmt.Errorf("read OpenSea page: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return OpenSeaDrop{}, fmt.Errorf("OpenSea returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, openSeaMaxPageBytes+1))
	if err != nil {
		return OpenSeaDrop{}, err
	}
	if len(body) > openSeaMaxPageBytes {
		return OpenSeaDrop{}, errors.New("the OpenSea page is too large to parse safely")
	}
	return parseOpenSeaHTML(canonical, slug, body)
}

func normalizeOpenSeaURL(rawURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" {
		return "", "", errors.New("enter an https://opensea.io/collection/... URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "opensea.io" && host != "www.opensea.io" {
		return "", "", errors.New("the URL must come from opensea.io")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || parts[0] != "collection" {
		return "", "", errors.New("only OpenSea collection mint URLs are currently supported")
	}
	slug, err := url.PathUnescape(parts[1])
	if err != nil || slug == "" || strings.ContainsAny(slug, "/\\") {
		return "", "", errors.New("the OpenSea collection slug is invalid")
	}
	return "https://opensea.io/collection/" + url.PathEscape(slug), slug, nil
}

func parseOpenSeaHTML(canonical, slug string, body []byte) (OpenSeaDrop, error) {
	var aggregate openSeaCollectionPayload
	for _, raw := range extractJSONObjectValues(body, []byte(`"collectionBySlug":`)) {
		var candidate openSeaCollectionPayload
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&candidate) != nil {
			continue
		}
		if candidate.Slug != "" && candidate.Slug != slug {
			continue
		}
		if candidate.Chain.Identifier != "" && candidate.Chain.Identifier != "robinhood" {
			continue
		}
		mergeOpenSeaCollection(&aggregate, candidate)
	}
	if aggregate.DropBySlug == nil {
		for _, raw := range extractJSONObjectValues(body, []byte(`"dropBySlug":`)) {
			var drop openSeaDropPayload
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if decoder.Decode(&drop) == nil && len(drop.Stages) > 0 {
				aggregate.DropBySlug = &drop
				break
			}
		}
	}
	if aggregate.Chain.Identifier != "robinhood" {
		return OpenSeaDrop{}, errors.New("this OpenSea collection is not on Robinhood Chain")
	}
	if !common.IsHexAddress(aggregate.Address) {
		return OpenSeaDrop{}, errors.New("the collection contract cannot be verified from the OpenSea page")
	}
	if aggregate.DropBySlug == nil || len(aggregate.DropBySlug.Stages) == 0 {
		return OpenSeaDrop{}, errors.New("the OpenSea page has no parseable mint stages")
	}
	drop := OpenSeaDrop{
		URL: canonical, Slug: slug, Name: aggregate.Name, Chain: "robinhood",
		Contract: common.HexToAddress(aggregate.Address).Hex(), Verified: aggregate.IsVerified,
		Approved: aggregate.IsApproved, ExternalURL: aggregate.ExternalURL,
		TwitterUsername: aggregate.TwitterUsername, MaxSupply: aggregate.DropBySlug.MaxSupply,
		TotalSupply: aggregate.DropBySlug.TotalSupply, Stages: make([]OpenSeaStage, 0, len(aggregate.DropBySlug.Stages)),
		RiskFlags: make([]string, 0),
	}
	for _, source := range aggregate.DropBySlug.Stages {
		price := source.Price.Token.Unit.String()
		if price == "" {
			price = "0"
		}
		priceWei, conversionErr := decimalToWei(price, 18)
		if conversionErr != nil {
			return OpenSeaDrop{}, fmt.Errorf("invalid OpenSea stage price: %w", conversionErr)
		}
		stage := OpenSeaStage{
			Label: source.Label, StageType: source.StageType, StageIndex: source.StageIndex,
			StartTime: source.StartTime, EndTime: source.EndTime,
			MaxTotalMintableByWallet: source.MaxTotalMintableByWallet,
			Price:                    price, PriceWei: priceWei.String(), PriceSymbol: source.Price.Token.Symbol,
			PriceContract:     source.Price.Token.ContractAddress,
			AutoMintSupported: source.StageType == "PUBLIC_SALE" || source.StageType == "SIGNED_PRESALE" || source.StageType == "MERKLE_PRESALE",
		}
		if source.Price.Token.Chain.Identifier != "" && source.Price.Token.Chain.Identifier != "robinhood" {
			stage.AutoMintSupported = false
			stage.BlockReason = "the stage price token is not on Robinhood Chain"
		} else if stage.PriceContract != "" && common.HexToAddress(stage.PriceContract) != (common.Address{}) {
			stage.AutoMintSupported = false
			stage.BlockReason = "only native-token SeaDrop mint prices are supported"
		} else if !stage.AutoMintSupported {
			stage.BlockReason = "this OpenSea stage type is not supported"
		}
		drop.Stages = append(drop.Stages, stage)
	}
	if !drop.Verified {
		drop.RiskFlags = append(drop.RiskFlags, "collection is not verified by OpenSea")
	}
	if !drop.Approved {
		drop.RiskFlags = append(drop.RiskFlags, "collection is not marked approved by OpenSea")
	}
	return drop, nil
}

func mergeOpenSeaCollection(target *openSeaCollectionPayload, source openSeaCollectionPayload) {
	if source.Slug != "" {
		target.Slug = source.Slug
	}
	if source.Name != "" {
		target.Name = source.Name
	}
	if source.Address != "" {
		target.Address = source.Address
	}
	if source.ExternalURL != "" {
		target.ExternalURL = source.ExternalURL
	}
	if source.TwitterUsername != "" {
		target.TwitterUsername = source.TwitterUsername
	}
	if source.Chain.Identifier != "" {
		target.Chain = source.Chain
	}
	target.IsVerified = target.IsVerified || source.IsVerified
	target.IsApproved = target.IsApproved || source.IsApproved
	if source.DropBySlug != nil && len(source.DropBySlug.Stages) > 0 {
		target.DropBySlug = source.DropBySlug
	}
}

func extractJSONObjectValues(input, marker []byte) [][]byte {
	var result [][]byte
	for offset := 0; offset < len(input); {
		index := bytes.Index(input[offset:], marker)
		if index < 0 {
			break
		}
		start := offset + index + len(marker)
		for start < len(input) && (input[start] == ' ' || input[start] == '\n' || input[start] == '\r' || input[start] == '\t') {
			start++
		}
		if start >= len(input) || input[start] != '{' {
			offset = start
			continue
		}
		end, ok := balancedJSONObjectEnd(input, start)
		if ok {
			result = append(result, input[start:end])
			offset = end
		} else {
			offset = start + 1
		}
	}
	return result
}

func balancedJSONObjectEnd(input []byte, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for index := start; index < len(input); index++ {
		character := input[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if character == '\\' {
				escaped = true
				continue
			}
			if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			continue
		}
		switch character {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1, true
			}
		}
	}
	return 0, false
}

func decimalToWei(value string, decimals int) (*big.Int, error) {
	rational, ok := new(big.Rat).SetString(strings.TrimSpace(value))
	if !ok || rational.Sign() < 0 {
		return nil, errors.New("amount must be a valid non-negative decimal")
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(rational, new(big.Rat).SetInt(scale))
	if !scaled.IsInt() {
		return nil, fmt.Errorf("amount supports at most %d decimal places", decimals)
	}
	return new(big.Int).Set(scaled.Num()), nil
}

func (a *App) CreateOpenSeaTasks(input OpenSeaTaskInput) ([]MintTask, error) {
	drop, err := a.InspectOpenSeaDrop(input.OpenSeaURL)
	if err != nil {
		return nil, err
	}
	if input.QuantityPerWallet == 0 {
		return nil, errors.New("mint quantity per wallet must be greater than zero")
	}
	now := time.Now()
	stages := make([]OpenSeaStage, 0, len(drop.Stages))
	var maximumWalletLimit uint64
	var highestPriceWei big.Int
	for _, stage := range drop.Stages {
		if !supportedOpenSeaStage(stage) {
			continue
		}
		end, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
		if endErr != nil || !now.Before(end) {
			continue
		}
		stages = append(stages, stage)
		if stage.MaxTotalMintableByWallet > maximumWalletLimit {
			maximumWalletLimit = stage.MaxTotalMintableByWallet
		}
		price := mustBigInt(stage.PriceWei)
		if price.Cmp(&highestPriceWei) > 0 {
			highestPriceWei.Set(price)
		}
	}
	if len(stages) == 0 {
		return nil, errors.New("the OpenSea page has no supported mint stages that are still open")
	}
	if maximumWalletLimit > 0 && input.QuantityPerWallet > maximumWalletLimit {
		return nil, fmt.Errorf("target quantity exceeds the highest OpenSea wallet limit of %d across all stages", maximumWalletLimit)
	}
	if len(input.WalletAddresses) == 0 {
		return nil, errors.New("select at least one local wallet")
	}
	pollIntervalMilliseconds := input.PollIntervalMilliseconds
	if pollIntervalMilliseconds == 0 && input.PollIntervalSeconds > 0 {
		pollIntervalMilliseconds = input.PollIntervalSeconds * 1000
	}
	if !isSupportedPollingSpeed(pollIntervalMilliseconds) {
		return nil, errors.New("select a polling speed of 100ms, 500ms, 2s, or 5s")
	}
	if strings.TrimSpace(input.RPCURL) == "" {
		input.RPCURL = defaultRobinhoodRPC
	}
	if _, err := url.ParseRequestURI(input.RPCURL); err != nil {
		return nil, errors.New("Robinhood RPC URL is invalid")
	}
	maxGasPrice, err := decimalToWei(input.MaxGasPriceGwei, 9)
	if err != nil || maxGasPrice.Sign() <= 0 {
		return nil, errors.New("maximum gas price in Gwei must be positive")
	}
	maxTotalCost, err := decimalToWei(input.MaxTotalCostETH, 18)
	if err != nil || maxTotalCost.Sign() <= 0 {
		return nil, errors.New("maximum total cost per wallet in ETH must be positive")
	}
	worstCaseMintValue := new(big.Int).Mul(new(big.Int).SetUint64(input.QuantityPerWallet), &highestPriceWei)
	if maxTotalCost.Cmp(worstCaseMintValue) < 0 {
		return nil, errors.New("maximum total cost per wallet is below the highest possible mint value for this multi-stage strategy")
	}
	seen := make(map[common.Address]bool)
	wallets := make([]common.Address, 0, len(input.WalletAddresses))
	for _, raw := range input.WalletAddresses {
		if !common.IsHexAddress(raw) {
			return nil, fmt.Errorf("invalid wallet address: %s", raw)
		}
		address := common.HexToAddress(raw)
		if seen[address] {
			continue
		}
		if !a.hasWallet(address) {
			return nil, fmt.Errorf("wallet %s has not been imported into the local application", address.Hex())
		}
		seen[address] = true
		wallets = append(wallets, address)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = drop.Name
	}
	created := make([]MintTask, 0, len(wallets))
	for _, wallet := range wallets {
		id, idErr := newTaskID()
		if idErr != nil {
			return nil, idErr
		}
		created = append(created, MintTask{
			ID: id, Name: name,
			OpenSeaURL: drop.URL, WalletAddress: wallet.Hex(),
			Network:  ChainConfig{Name: "Robinhood Chain", ChainID: robinhoodChainID, RPCURL: input.RPCURL},
			Contract: drop.Contract,
			SaleGate: SaleGate{Function: "opensea_seadrop_strategy", Expect: true, PollIntervalMilliseconds: pollIntervalMilliseconds},
			Mint:     MintConfig{Method: "SeaDrop multi-stage strategy", Quantity: input.QuantityPerWallet, ValueWei: worstCaseMintValue.String(), MaxGasPriceWei: maxGasPrice.String(), MaxTotalCostWei: maxTotalCost.String()},
			OpenSea:  &OpenSeaTaskSnapshot{Slug: drop.Slug, Stages: append([]OpenSeaStage(nil), stages...), TargetQuantity: input.QuantityPerWallet, Collection: drop.Name},
			Enabled:  true, Status: "ready",
		})
	}
	a.mu.Lock()
	for _, existing := range a.tasks {
		if !existing.Enabled || existing.OpenSea == nil || existing.OpenSea.Slug != drop.Slug {
			continue
		}
		for _, candidate := range created {
			if common.HexToAddress(existing.WalletAddress) == common.HexToAddress(candidate.WalletAddress) {
				a.mu.Unlock()
				return nil, fmt.Errorf("wallet %s already has an enabled strategy for this OpenSea collection", candidate.WalletAddress)
			}
		}
	}
	a.tasks = append(a.tasks, created...)
	err = a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return created, nil
}

func mustBigInt(value string) *big.Int {
	result, ok := new(big.Int).SetString(value, 10)
	if !ok {
		return new(big.Int)
	}
	return result
}

const openSeaMintQuoteQuery = `
query MintDeskMintQuote($address: Address!, $fromAssets: [AssetQuantityInput!]!, $toAssets: [AssetQuantityInput!]!, $capabilities: WalletCapabilities) {
  swap(address: $address, fromAssets: $fromAssets, toAssets: $toAssets, action: MINT, capabilities: $capabilities) {
    actions {
      __typename
      ... on TransactionAction {
        transactionSubmissionData {
          to
          data
          value
          chain { identifier networkId }
        }
      }
    }
    errors { __typename }
  }
}`

type openSeaQuoteCoordinator struct {
	mu   sync.Mutex
	next time.Time
}

var sharedOpenSeaQuotes openSeaQuoteCoordinator

func (c *openSeaQuoteCoordinator) wait(ctx context.Context) error {
	now := time.Now()
	c.mu.Lock()
	reserved := now
	if c.next.After(reserved) {
		reserved = c.next
	}
	c.next = reserved.Add(openSeaQuoteMinInterval)
	c.mu.Unlock()
	if !reserved.After(now) {
		return nil
	}
	timer := time.NewTimer(reserved.Sub(now))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type openSeaMintTransaction struct {
	To              string
	Data            string
	Value           string
	ChainIdentifier string
	ChainID         int64
}

type openSeaMintQuoteError struct {
	Codes []string
}

func (e *openSeaMintQuoteError) Error() string {
	if len(e.Codes) == 0 {
		return "OpenSea returned no executable mint transaction"
	}
	return "OpenSea mint quote unavailable: " + strings.Join(e.Codes, ", ")
}

func requestOpenSeaMintQuote(ctx context.Context, wallet, nft common.Address, quantity uint64) (openSeaMintTransaction, error) {
	variables := map[string]any{
		"address": wallet.Hex(),
		"fromAssets": []any{map[string]any{"asset": map[string]any{
			"contractAddress": common.Address{}.Hex(), "chain": "robinhood",
		}}},
		"toAssets": []any{map[string]any{
			"asset":    map[string]any{"contractAddress": nft.Hex(), "chain": "robinhood", "tokenId": "0"},
			"quantity": fmt.Sprintf("%d", quantity),
		}},
		"capabilities": map[string]any{"eip7702": false},
	}
	payload, err := json.Marshal(map[string]any{
		"operationName": "MintDeskMintQuote", "query": openSeaMintQuoteQuery, "variables": variables,
	})
	if err != nil {
		return openSeaMintTransaction{}, err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := sharedOpenSeaQuotes.wait(ctx); err != nil {
			return openSeaMintTransaction{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openSeaGraphQLEndpoint, bytes.NewReader(payload))
		if err != nil {
			return openSeaMintTransaction{}, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Origin", "https://opensea.io")
		req.Header.Set("Referer", "https://opensea.io/")
		req.Header.Set("User-Agent", "MintDesk/1.0 (+local OpenSea mint client)")
		response, requestErr := http.DefaultClient.Do(req)
		if requestErr != nil {
			lastErr = requestErr
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
			response.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
				lastErr = fmt.Errorf("OpenSea returned HTTP %d", response.StatusCode)
			} else if response.StatusCode != http.StatusOK {
				return openSeaMintTransaction{}, fmt.Errorf("OpenSea returned HTTP %d", response.StatusCode)
			} else {
				return parseOpenSeaMintQuote(body)
			}
		}
		if attempt < 3 {
			timer := time.NewTimer(time.Duration(1<<attempt) * 500 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return openSeaMintTransaction{}, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return openSeaMintTransaction{}, fmt.Errorf("OpenSea mint quote request failed: %w", lastErr)
}

func parseOpenSeaMintQuote(body []byte) (openSeaMintTransaction, error) {
	var response struct {
		Data *struct {
			Swap struct {
				Actions []struct {
					TypeName                  string `json:"__typename"`
					TransactionSubmissionData *struct {
						To    string `json:"to"`
						Data  string `json:"data"`
						Value string `json:"value"`
						Chain struct {
							Identifier string `json:"identifier"`
							NetworkID  int64  `json:"networkId"`
						} `json:"chain"`
					} `json:"transactionSubmissionData"`
				} `json:"actions"`
				Errors []struct {
					TypeName string `json:"__typename"`
				} `json:"errors"`
			} `json:"swap"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return openSeaMintTransaction{}, fmt.Errorf("decode OpenSea mint quote: %w", err)
	}
	if response.Data == nil {
		messages := make([]string, 0, len(response.Errors))
		for _, item := range response.Errors {
			if item.Message != "" {
				messages = append(messages, item.Message)
			}
		}
		return openSeaMintTransaction{}, &openSeaMintQuoteError{Codes: messages}
	}
	if len(response.Data.Swap.Errors) > 0 {
		codes := make([]string, 0, len(response.Data.Swap.Errors))
		for _, item := range response.Data.Swap.Errors {
			codes = append(codes, item.TypeName)
		}
		return openSeaMintTransaction{}, &openSeaMintQuoteError{Codes: codes}
	}
	var transactions []openSeaMintTransaction
	for _, action := range response.Data.Swap.Actions {
		if action.TypeName != "TransactionAction" || action.TransactionSubmissionData == nil {
			return openSeaMintTransaction{}, errors.New("OpenSea returned a non-transaction mint action")
		}
		data := action.TransactionSubmissionData
		transactions = append(transactions, openSeaMintTransaction{
			To: data.To, Data: data.Data, Value: data.Value,
			ChainIdentifier: data.Chain.Identifier, ChainID: data.Chain.NetworkID,
		})
	}
	if len(transactions) != 1 {
		return openSeaMintTransaction{}, fmt.Errorf("OpenSea returned %d mint transactions; exactly one same-chain transaction is required", len(transactions))
	}
	return transactions[0], nil
}

type openSeaQuoteRequester func(context.Context, common.Address, common.Address, uint64) (openSeaMintTransaction, error)

func selectOpenSeaMintQuote(ctx context.Context, requester openSeaQuoteRequester, wallet, nft common.Address, maximum uint64) (openSeaMintTransaction, uint64, error) {
	lowestQuote, lowestErr := requester(ctx, wallet, nft, 1)
	if lowestErr != nil {
		return openSeaMintTransaction{}, 0, lowestErr
	}
	if maximum <= 1 {
		return lowestQuote, 1, nil
	}
	quote, err := requester(ctx, wallet, nft, maximum)
	if err == nil {
		return quote, maximum, nil
	}
	if retryableOpenSeaQuoteError(err) {
		return openSeaMintTransaction{}, 0, err
	}
	bestQuote, bestQuantity := lowestQuote, uint64(1)
	low, high := uint64(2), maximum-1
	for low <= high {
		mid := low + (high-low)/2
		candidate, candidateErr := requester(ctx, wallet, nft, mid)
		if candidateErr == nil {
			bestQuote, bestQuantity = candidate, mid
			low = mid + 1
		} else if retryableOpenSeaQuoteError(candidateErr) {
			return openSeaMintTransaction{}, 0, candidateErr
		} else {
			high = mid - 1
		}
	}
	return bestQuote, bestQuantity, nil
}

func retryableOpenSeaQuoteError(err error) bool {
	var quoteErr *openSeaMintQuoteError
	if !errors.As(err, &quoteErr) {
		return false
	}
	for _, code := range quoteErr.Codes {
		lower := strings.ToLower(code)
		if strings.Contains(lower, "notminting") || strings.Contains(lower, "notstarted") || strings.Contains(lower, "noactive") || strings.Contains(lower, "temporar") || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many") || strings.Contains(lower, "timeout") || strings.Contains(lower, "unavailable") {
			return true
		}
	}
	return false
}

func unavailableOpenSeaStageQuoteError(err error) bool {
	var quoteErr *openSeaMintQuoteError
	if !errors.As(err, &quoteErr) {
		return false
	}
	for _, code := range quoteErr.Codes {
		lower := strings.ToLower(code)
		if strings.Contains(lower, "notminting") || strings.Contains(lower, "notstarted") || strings.Contains(lower, "noactive") {
			return true
		}
	}
	return false
}

var (
	seaDropABI = mustParseABI(`[
		{"type":"function","name":"getPublicDrop","stateMutability":"view","inputs":[{"name":"nftContract","type":"address"}],"outputs":[{"name":"","type":"tuple","components":[{"name":"mintPrice","type":"uint80"},{"name":"startTime","type":"uint48"},{"name":"endTime","type":"uint48"},{"name":"maxTotalMintableByWallet","type":"uint16"},{"name":"feeBps","type":"uint16"},{"name":"restrictFeeRecipients","type":"bool"}]}]},
		{"type":"function","name":"getAllowedFeeRecipients","stateMutability":"view","inputs":[{"name":"nftContract","type":"address"}],"outputs":[{"name":"","type":"address[]"}]},
		{"type":"function","name":"mintPublic","stateMutability":"payable","inputs":[{"name":"nftContract","type":"address"},{"name":"feeRecipient","type":"address"},{"name":"minterIfNotPayer","type":"address"},{"name":"quantity","type":"uint256"}],"outputs":[]},
		{"type":"function","name":"mintAllowList","stateMutability":"payable","inputs":[{"name":"nftContract","type":"address"},{"name":"feeRecipient","type":"address"},{"name":"minterIfNotPayer","type":"address"},{"name":"quantity","type":"uint256"},{"name":"mintParams","type":"tuple","components":[{"name":"mintPrice","type":"uint256"},{"name":"maxTotalMintableByWallet","type":"uint256"},{"name":"startTime","type":"uint256"},{"name":"endTime","type":"uint256"},{"name":"dropStageIndex","type":"uint256"},{"name":"maxTokenSupplyForStage","type":"uint256"},{"name":"feeBps","type":"uint256"},{"name":"restrictFeeRecipients","type":"bool"}]},{"name":"proof","type":"bytes32[]"}],"outputs":[]},
		{"type":"function","name":"mintSigned","stateMutability":"payable","inputs":[{"name":"nftContract","type":"address"},{"name":"feeRecipient","type":"address"},{"name":"minterIfNotPayer","type":"address"},{"name":"quantity","type":"uint256"},{"name":"mintParams","type":"tuple","components":[{"name":"mintPrice","type":"uint256"},{"name":"maxTotalMintableByWallet","type":"uint256"},{"name":"startTime","type":"uint256"},{"name":"endTime","type":"uint256"},{"name":"dropStageIndex","type":"uint256"},{"name":"maxTokenSupplyForStage","type":"uint256"},{"name":"feeBps","type":"uint256"},{"name":"restrictFeeRecipients","type":"bool"}]},{"name":"salt","type":"uint256"},{"name":"signature","type":"bytes"}],"outputs":[]}
	]`)
	seaDropTokenABI = mustParseABI(`[
		{"type":"function","name":"getMintStats","stateMutability":"view","inputs":[{"name":"minter","type":"address"}],"outputs":[{"name":"minterNumMinted","type":"uint256"},{"name":"currentTotalSupply","type":"uint256"},{"name":"maxSupply","type":"uint256"}]},
		{"type":"event","name":"AllowedSeaDropUpdated","anonymous":false,"inputs":[{"name":"allowedSeaDrop","type":"address[]","indexed":false}]}
	]`)
)

func mustParseABI(definition string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		panic(err)
	}
	return parsed
}

type seaDropPublicConfig struct {
	MintPrice                *big.Int
	StartTime                *big.Int
	EndTime                  *big.Int
	MaxTotalMintableByWallet uint16
	FeeBps                   uint16
	RestrictFeeRecipients    bool
}

type seaDropMintParams struct {
	MintPrice                *big.Int
	MaxTotalMintableByWallet *big.Int
	StartTime                *big.Int
	EndTime                  *big.Int
	DropStageIndex           *big.Int
	MaxTokenSupplyForStage   *big.Int
	FeeBps                   *big.Int
	RestrictFeeRecipients    bool
}

func callContractABI(ctx context.Context, client *ethclient.Client, endpoint string, target common.Address, contractABI abi.ABI, method string, arguments ...any) ([]any, error) {
	data, err := contractABI.Pack(method, arguments...)
	if err != nil {
		return nil, err
	}
	result, err := rpcRead(ctx, endpoint, func() ([]byte, error) {
		return client.CallContract(ctx, ethereum.CallMsg{To: &target, Data: data}, nil)
	})
	if err != nil {
		return nil, err
	}
	return contractABI.Unpack(method, result)
}

func findSeaDrop(ctx context.Context, client *ethclient.Client, endpoint string, nft common.Address) (common.Address, error) {
	event := seaDropTokenABI.Events["AllowedSeaDropUpdated"]
	logs, err := rpcRead(ctx, endpoint, func() ([]types.Log, error) {
		return client.FilterLogs(ctx, ethereum.FilterQuery{Addresses: []common.Address{nft}, Topics: [][]common.Hash{{event.ID}}})
	})
	if err == nil {
		for index := len(logs) - 1; index >= 0; index-- {
			values, unpackErr := event.Inputs.NonIndexed().Unpack(logs[index].Data)
			if unpackErr != nil || len(values) != 1 {
				continue
			}
			addresses, ok := values[0].([]common.Address)
			if ok && len(addresses) > 0 {
				return addresses[0], nil
			}
		}
	}
	// OpenSea's Robinhood SeaDrop deployment. It is still validated by bytecode and getPublicDrop before use.
	fallback := common.HexToAddress("0x00005EA00Ac477B1030CE78506496e8C2dE24bf5")
	code, codeErr := rpcRead(ctx, endpoint, func() ([]byte, error) {
		return client.CodeAt(ctx, fallback, nil)
	})
	if codeErr == nil && len(code) > 0 {
		return fallback, nil
	}
	if err != nil {
		return common.Address{}, fmt.Errorf("read AllowedSeaDropUpdated: %w", err)
	}
	return common.Address{}, errors.New("could not discover a SeaDrop address from the NFT contract")
}

func readPublicDrop(ctx context.Context, client *ethclient.Client, endpoint string, seaDrop, nft common.Address) (seaDropPublicConfig, error) {
	values, err := callContractABI(ctx, client, endpoint, seaDrop, seaDropABI, "getPublicDrop", nft)
	if err != nil || len(values) != 1 {
		if err == nil {
			err = errors.New("SeaDrop returned an unexpected value count")
		}
		return seaDropPublicConfig{}, err
	}
	converted := abi.ConvertType(values[0], new(seaDropPublicConfig))
	config, ok := converted.(*seaDropPublicConfig)
	if !ok {
		return seaDropPublicConfig{}, errors.New("could not decode the SeaDrop public drop")
	}
	return *config, nil
}

func readAllowedFeeRecipients(ctx context.Context, client *ethclient.Client, endpoint string, seaDrop, nft common.Address) ([]common.Address, error) {
	values, err := callContractABI(ctx, client, endpoint, seaDrop, seaDropABI, "getAllowedFeeRecipients", nft)
	if err != nil || len(values) != 1 {
		return nil, err
	}
	recipients, ok := values[0].([]common.Address)
	if !ok {
		return nil, errors.New("could not decode SeaDrop fee recipients")
	}
	return recipients, nil
}

func readMintStats(ctx context.Context, client *ethclient.Client, endpoint string, nft, minter common.Address) (*big.Int, *big.Int, *big.Int, error) {
	values, err := callContractABI(ctx, client, endpoint, nft, seaDropTokenABI, "getMintStats", minter)
	if err != nil || len(values) != 3 {
		return nil, nil, nil, err
	}
	minted, ok1 := values[0].(*big.Int)
	total, ok2 := values[1].(*big.Int)
	maximum, ok3 := values[2].(*big.Int)
	if !ok1 || !ok2 || !ok3 {
		return nil, nil, nil, errors.New("could not decode NFT mint stats")
	}
	return minted, total, maximum, nil
}

func validateOpenSeaAllowlistQuote(quote openSeaMintTransaction, stage OpenSeaStage, wallet, nft, seaDrop common.Address, quantity, alreadyMinted uint64, allowedRecipients []common.Address) ([]byte, *big.Int, error) {
	if quote.ChainIdentifier != "robinhood" || (quote.ChainID != 0 && quote.ChainID != robinhoodChainID) {
		return nil, nil, errors.New("OpenSea mint quote is for a different chain")
	}
	if !common.IsHexAddress(quote.To) || common.HexToAddress(quote.To) != seaDrop {
		return nil, nil, errors.New("OpenSea mint quote target is not the collection's verified SeaDrop contract")
	}
	data := common.FromHex(quote.Data)
	if len(data) < 4 {
		return nil, nil, errors.New("OpenSea mint quote calldata is invalid")
	}
	method, err := seaDropABI.MethodById(data[:4])
	if err != nil {
		return nil, nil, errors.New("OpenSea mint quote does not call a supported SeaDrop method")
	}
	expectedMethod := "mintSigned"
	if stage.StageType == "MERKLE_PRESALE" {
		expectedMethod = "mintAllowList"
	}
	if method.Name != expectedMethod {
		return nil, nil, fmt.Errorf("OpenSea returned %s for a %s stage", method.Name, stage.StageType)
	}
	arguments, err := method.Inputs.Unpack(data[4:])
	if err != nil || len(arguments) < 6 {
		return nil, nil, errors.New("OpenSea mint quote arguments could not be decoded")
	}
	quotedNFT, okNFT := arguments[0].(common.Address)
	feeRecipient, okRecipient := arguments[1].(common.Address)
	minter, okMinter := arguments[2].(common.Address)
	quotedQuantity, okQuantity := arguments[3].(*big.Int)
	if !okNFT || !okRecipient || !okMinter || !okQuantity || quotedNFT != nft || minter != wallet || quotedQuantity.Uint64() != quantity || !quotedQuantity.IsUint64() {
		return nil, nil, errors.New("OpenSea mint quote collection, wallet, or quantity does not match the task")
	}
	recipientAllowed := false
	for _, candidate := range allowedRecipients {
		if candidate == feeRecipient && candidate != (common.Address{}) {
			recipientAllowed = true
			break
		}
	}
	if !recipientAllowed {
		return nil, nil, errors.New("OpenSea mint quote uses an unverified fee recipient")
	}
	converted := abi.ConvertType(arguments[4], new(seaDropMintParams))
	params, ok := converted.(*seaDropMintParams)
	if !ok || params.MintPrice == nil || params.MaxTotalMintableByWallet == nil || params.StartTime == nil || params.EndTime == nil || params.DropStageIndex == nil {
		return nil, nil, errors.New("OpenSea mint parameters could not be decoded")
	}
	pageStart, startErr := time.Parse(time.RFC3339Nano, stage.StartTime)
	pageEnd, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
	pagePrice := mustBigInt(stage.PriceWei)
	if startErr != nil || endErr != nil || params.StartTime.Int64() != pageStart.Unix() || params.EndTime.Int64() != pageEnd.Unix() || params.DropStageIndex.Int64() != int64(stage.StageIndex) || params.MintPrice.Cmp(pagePrice) != 0 {
		return nil, nil, errors.New("OpenSea mint quote price, time, or stage index changed; inspect and recreate the task")
	}
	requestedTotal := new(big.Int).Add(new(big.Int).SetUint64(alreadyMinted), new(big.Int).SetUint64(quantity))
	if requestedTotal.Cmp(params.MaxTotalMintableByWallet) > 0 {
		return nil, nil, &openSeaMintQuoteError{Codes: []string{"WalletAllowanceExhausted"}}
	}
	if method.Name == "mintAllowList" {
		proof, ok := arguments[5].([][32]byte)
		if !ok || len(proof) == 0 {
			return nil, nil, errors.New("OpenSea Merkle mint quote has no proof")
		}
	} else {
		if len(arguments) < 7 {
			return nil, nil, errors.New("OpenSea signed mint quote is incomplete")
		}
		signature, ok := arguments[6].([]byte)
		if !ok || len(signature) == 0 {
			return nil, nil, errors.New("OpenSea signed mint quote has no signature")
		}
	}
	value, ok := new(big.Int).SetString(quote.Value, 10)
	if !ok || value.Sign() < 0 {
		return nil, nil, errors.New("OpenSea mint quote value is invalid")
	}
	expectedValue := new(big.Int).Mul(new(big.Int).Set(params.MintPrice), quotedQuantity)
	if value.Cmp(expectedValue) != 0 {
		return nil, nil, errors.New("OpenSea mint quote value does not match its signed price")
	}
	return data, value, nil
}

func waitOpenSeaReceipt(ctx context.Context, client *ethclient.Client, endpoint string, hash common.Hash) (*types.Receipt, error) {
	for {
		receipt, err := rpcRead(ctx, endpoint, func() (*types.Receipt, error) {
			return client.TransactionReceipt(ctx, hash)
		})
		if err == nil {
			return receipt, nil
		}
		if !errors.Is(err, ethereum.NotFound) && !isRetryableRPCError(err) {
			return nil, err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *App) setOpenSeaTaskSpent(id string, spent *big.Int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for index := range a.tasks {
		if a.tasks[index].ID == id && a.tasks[index].OpenSea != nil {
			a.tasks[index].OpenSea.SpentWei = spent.String()
			_ = a.saveLocked()
			return
		}
	}
}

func (a *App) runOpenSeaTask(ctx context.Context, client *ethclient.Client, task MintTask, account accounts.Account, chainID *big.Int) {
	if task.Network.ChainID != robinhoodChainID || task.OpenSea == nil {
		a.recordTask(task.ID, "failed", false, "", "task is not a supported Robinhood OpenSea strategy")
		return
	}
	nft := common.HexToAddress(task.Contract)
	seaDrop, err := findSeaDrop(ctx, client, task.Network.RPCURL, nft)
	if err != nil {
		a.recordTask(task.ID, "failed", false, "", err.Error())
		return
	}
	target := openSeaTaskTarget(task)
	stages := openSeaTaskStages(task.OpenSea)
	maximumTotalCost := mustBigInt(task.Mint.MaxTotalCostWei)
	spent := openSeaTaskSpent(task.OpenSea)
	interval := taskPollInterval(task.SaleGate)
	for _, stage := range stages {
		if !supportedOpenSeaStage(stage) {
			continue
		}
		stageStart, startErr := time.Parse(time.RFC3339Nano, stage.StartTime)
		stageEnd, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
		if startErr != nil || endErr != nil || !stageEnd.After(stageStart) {
			a.recordTask(task.ID, "failed", false, "", "an OpenSea stage time is invalid; recreate the task")
			return
		}
		if time.Now().Before(stageStart) {
			a.recordTask(task.ID, "scheduled", true, task.LastTxHash, "waiting for "+stage.Label)
			timer := time.NewTimer(time.Until(stageStart))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		semanticFailures := 0
		for time.Now().Before(stageEnd) {
			minted, _, _, statsErr := readMintStats(ctx, client, task.Network.RPCURL, nft, account.Address)
			a.checkedTask(task.ID)
			if statsErr != nil {
				if !isRetryableRPCError(statsErr) {
					a.recordTask(task.ID, "failed", false, task.LastTxHash, "read wallet mint progress: "+statsErr.Error())
					return
				}
			} else if minted.IsUint64() && minted.Uint64() >= target {
				a.recordTask(task.ID, "broadcast", false, task.LastTxHash, fmt.Sprintf("target reached: %d/%d minted", target, target))
				return
			} else if minted.IsUint64() {
				mintedQuantity := minted.Uint64()
				remaining := target - mintedQuantity
				stageAllowance := remaining
				if stage.MaxTotalMintableByWallet > 0 {
					if mintedQuantity >= stage.MaxTotalMintableByWallet {
						break
					}
					stageAllowance = minUint64(stageAllowance, stage.MaxTotalMintableByWallet-mintedQuantity)
				}
				var hash string
				var mintErr error
				var transactionValue *big.Int
				remainingBudget := new(big.Int).Sub(new(big.Int).Set(maximumTotalCost), spent)
				if remainingBudget.Sign() <= 0 {
					a.recordTask(task.ID, "failed", false, task.LastTxHash, "the strategy has exhausted its maximum total cost per wallet")
					return
				}
				broadcastTask := task
				broadcastTask.Mint.MaxTotalCostWei = remainingBudget.String()
				if stage.StageType == "PUBLIC_SALE" {
					config, configErr := readPublicDrop(ctx, client, task.Network.RPCURL, seaDrop, nft)
					if configErr != nil {
						mintErr = configErr
					} else if config.StartTime == nil || config.EndTime == nil || !time.Unix(config.StartTime.Int64(), 0).Equal(stageStart) || !time.Unix(config.EndTime.Int64(), 0).Equal(stageEnd) {
						a.recordTask(task.ID, "failed", false, task.LastTxHash, "the on-chain Public schedule changed; inspect and recreate the task")
						return
					} else {
						if config.MintPrice.Cmp(mustBigInt(stage.PriceWei)) != 0 || (stage.MaxTotalMintableByWallet > 0 && uint64(config.MaxTotalMintableByWallet) != stage.MaxTotalMintableByWallet) {
							a.recordTask(task.ID, "failed", false, task.LastTxHash, "the on-chain Public price or wallet limit changed; inspect and recreate the task")
							return
						}
						if mintedQuantity >= uint64(config.MaxTotalMintableByWallet) {
							break
						}
						stageAllowance = minUint64(stageAllowance, uint64(config.MaxTotalMintableByWallet)-mintedQuantity)
						transactionValue = new(big.Int).Mul(new(big.Int).Set(config.MintPrice), new(big.Int).SetUint64(stageAllowance))
						hash, mintErr = a.broadcastOpenSeaPublic(ctx, client, account, broadcastTask, chainID, seaDrop, nft, config, stageAllowance)
					}
				} else {
					var quote openSeaMintTransaction
					var quotedQuantity uint64
					quote, quotedQuantity, mintErr = selectOpenSeaMintQuote(ctx, requestOpenSeaMintQuote, account.Address, nft, stageAllowance)
					if mintErr == nil {
						recipients, recipientErr := readAllowedFeeRecipients(ctx, client, task.Network.RPCURL, seaDrop, nft)
						if recipientErr != nil {
							mintErr = recipientErr
						} else {
							var data []byte
							var value *big.Int
							data, value, mintErr = validateOpenSeaAllowlistQuote(quote, stage, account.Address, nft, seaDrop, quotedQuantity, mintedQuantity, recipients)
							if mintErr == nil {
								transactionValue = value
								hash, mintErr = a.broadcastPrepared(ctx, client, account, broadcastTask, chainID, seaDrop, data, value)
							}
						}
					}
				}
				if mintErr == nil && hash != "" {
					task.LastTxHash = hash
					a.recordTask(task.ID, "watching", true, hash, fmt.Sprintf("transaction broadcast for %s; waiting for confirmation", stage.Label))
					receipt, receiptErr := waitOpenSeaReceipt(ctx, client, task.Network.RPCURL, common.HexToHash(hash))
					if receiptErr != nil {
						a.recordTask(task.ID, "failed", false, hash, "mint receipt failed: "+receiptErr.Error())
						return
					}
					if receipt.EffectiveGasPrice == nil || transactionValue == nil {
						a.recordTask(task.ID, "failed", false, hash, "the confirmed transaction cost could not be accounted for safely")
						return
					}
					actualCost := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPrice)
					if receipt.Status == types.ReceiptStatusSuccessful {
						actualCost.Add(actualCost, transactionValue)
					}
					spent.Add(spent, actualCost)
					a.setOpenSeaTaskSpent(task.ID, spent)
					if receipt.Status == types.ReceiptStatusSuccessful {
						a.recordTask(task.ID, "watching", true, hash, fmt.Sprintf("%s confirmed; checking remaining target", stage.Label))
						semanticFailures = 0
						continue
					}
					mintErr = errors.New("mint transaction reverted on-chain")
				}
				if _, semantic := mintErr.(*openSeaMintQuoteError); semantic {
					semanticFailures++
					if !retryableOpenSeaQuoteError(mintErr) || (unavailableOpenSeaStageQuoteError(mintErr) && (semanticFailures >= 3 || time.Now().After(stageStart.Add(10*time.Second)))) {
						message := fmt.Sprintf("wallet is not eligible for %s; waiting for the next stage", stage.Label)
						if unavailableOpenSeaStageQuoteError(mintErr) {
							message = fmt.Sprintf("OpenSea did not activate %s for this wallet; waiting for the next stage", stage.Label)
						}
						a.recordTask(task.ID, "watching", true, task.LastTxHash, message)
						break
					}
				} else if mintErr != nil && !isRetryableRPCError(mintErr) {
					a.recordTask(task.ID, "failed", false, task.LastTxHash, mintErr.Error())
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
			}
		}
	}
	minted, _, _, err := readMintStats(ctx, client, task.Network.RPCURL, nft, account.Address)
	if err == nil && minted.IsUint64() && minted.Uint64() >= target {
		a.recordTask(task.ID, "broadcast", false, task.LastTxHash, fmt.Sprintf("target reached: %d/%d minted", target, target))
		return
	}
	progress := "unknown"
	if err == nil {
		progress = minted.String()
	}
	a.recordTask(task.ID, "failed", false, task.LastTxHash, fmt.Sprintf("all OpenSea stages ended before the target was reached (%s/%d minted)", progress, target))
}

func minUint64(left, right uint64) uint64 {
	if left < right {
		return left
	}
	return right
}

func isSupportedPollingSpeed(milliseconds int) bool {
	switch milliseconds {
	case 100, 500, 2000, 5000:
		return true
	default:
		return false
	}
}

func (a *App) broadcastOpenSeaPublic(ctx context.Context, client *ethclient.Client, account accounts.Account, task MintTask, chainID *big.Int, seaDrop, nft common.Address, config seaDropPublicConfig, quantity uint64) (string, error) {
	state := a.nonces.state(task.Network.ChainID, account.Address)
	state.mu.Lock()
	defer state.mu.Unlock()

	minted, total, maximum, err := readMintStats(ctx, client, task.Network.RPCURL, nft, account.Address)
	if err != nil {
		return "", fmt.Errorf("read wallet mint allowance: %w", err)
	}
	requested := new(big.Int).SetUint64(quantity)
	if new(big.Int).Add(new(big.Int).Set(minted), requested).Cmp(new(big.Int).SetUint64(uint64(config.MaxTotalMintableByWallet))) > 0 {
		return "", fmt.Errorf("wallet already minted %s; adding %d would exceed the on-chain limit of %d", minted, quantity, config.MaxTotalMintableByWallet)
	}
	if new(big.Int).Add(new(big.Int).Set(total), requested).Cmp(maximum) > 0 {
		return "", errors.New("remaining NFT supply is insufficient")
	}
	recipients, err := readAllowedFeeRecipients(ctx, client, task.Network.RPCURL, seaDrop, nft)
	if err != nil {
		return "", fmt.Errorf("read OpenSea fee recipient: %w", err)
	}
	var feeRecipient common.Address
	for _, candidate := range recipients {
		if candidate != (common.Address{}) {
			feeRecipient = candidate
			break
		}
	}
	if feeRecipient == (common.Address{}) {
		return "", errors.New("SeaDrop has no verifiable fee recipient; refusing to guess an address")
	}
	data, err := seaDropABI.Pack("mintPublic", nft, feeRecipient, account.Address, requested)
	if err != nil {
		return "", err
	}
	value := new(big.Int).Mul(new(big.Int).Set(config.MintPrice), requested)
	return a.broadcastPreparedLocked(ctx, client, account, task, chainID, seaDrop, data, value, state)
}

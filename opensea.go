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
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	robinhoodChainID     int64 = 4663
	defaultRobinhoodRPC        = "https://rpc.mainnet.chain.robinhood.com"
	openSeaMaxPageBytes        = 12 << 20
	openSeaWatchLeadTime       = time.Minute
)

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
	Name                string   `json:"name"`
	OpenSeaURL          string   `json:"openSeaUrl"`
	WalletAddresses     []string `json:"walletAddresses"`
	StageIndex          int      `json:"stageIndex"`
	QuantityPerWallet   uint64   `json:"quantityPerWallet"`
	RPCURL              string   `json:"rpcUrl"`
	PollIntervalSeconds int      `json:"pollIntervalSeconds"`
	MaxGasPriceGwei     string   `json:"maxGasPriceGwei"`
	MaxTotalCostETH     string   `json:"maxTotalCostEth"`
}

type OpenSeaTaskSnapshot struct {
	Slug       string       `json:"slug"`
	Stage      OpenSeaStage `json:"stage"`
	Collection string       `json:"collection"`
}

func openSeaWatchDelay(stage OpenSeaStage, now time.Time) (time.Duration, time.Time, error) {
	start, startErr := time.Parse(time.RFC3339Nano, stage.StartTime)
	end, endErr := time.Parse(time.RFC3339Nano, stage.EndTime)
	if startErr != nil || endErr != nil || !end.After(start) {
		return 0, time.Time{}, errors.New("OpenSea 阶段时间无效，请重新创建任务")
	}
	if !now.Before(end) {
		return 0, end, errors.New("OpenSea 所选 mint 阶段已结束")
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
				Unit   json.Number `json:"unit"`
				Symbol string      `json:"symbol"`
				Chain  struct {
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
		return OpenSeaDrop{}, fmt.Errorf("读取 OpenSea 页面失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return OpenSeaDrop{}, fmt.Errorf("OpenSea 返回 HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, openSeaMaxPageBytes+1))
	if err != nil {
		return OpenSeaDrop{}, err
	}
	if len(body) > openSeaMaxPageBytes {
		return OpenSeaDrop{}, errors.New("OpenSea 页面过大，已停止解析")
	}
	return parseOpenSeaHTML(canonical, slug, body)
}

func normalizeOpenSeaURL(rawURL string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme != "https" {
		return "", "", errors.New("请输入 https://opensea.io/collection/... 链接")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "opensea.io" && host != "www.opensea.io" {
		return "", "", errors.New("链接必须来自 opensea.io")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) < 2 || parts[0] != "collection" {
		return "", "", errors.New("目前支持 OpenSea collection mint 链接")
	}
	slug, err := url.PathUnescape(parts[1])
	if err != nil || slug == "" || strings.ContainsAny(slug, "/\\") {
		return "", "", errors.New("OpenSea collection slug 无效")
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
		return OpenSeaDrop{}, errors.New("该 OpenSea 集合不是 Robinhood Chain")
	}
	if !common.IsHexAddress(aggregate.Address) {
		return OpenSeaDrop{}, errors.New("无法从 OpenSea 页面确认集合合约地址")
	}
	if aggregate.DropBySlug == nil || len(aggregate.DropBySlug.Stages) == 0 {
		return OpenSeaDrop{}, errors.New("OpenSea 页面没有可解析的 mint 阶段")
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
			return OpenSeaDrop{}, fmt.Errorf("OpenSea 阶段价格无效: %w", conversionErr)
		}
		stage := OpenSeaStage{
			Label: source.Label, StageType: source.StageType, StageIndex: source.StageIndex,
			StartTime: source.StartTime, EndTime: source.EndTime,
			MaxTotalMintableByWallet: source.MaxTotalMintableByWallet,
			Price:                    price, PriceWei: priceWei.String(), PriceSymbol: source.Price.Token.Symbol,
			AutoMintSupported: source.StageType == "PUBLIC_SALE",
		}
		if source.Price.Token.Chain.Identifier != "" && source.Price.Token.Chain.Identifier != "robinhood" {
			stage.AutoMintSupported = false
			stage.BlockReason = "阶段价格代币不在 Robinhood Chain"
		} else if source.StageType != "PUBLIC_SALE" {
			stage.BlockReason = "白名单阶段需要 OpenSea 为所选钱包下发签名或 Merkle proof"
		}
		drop.Stages = append(drop.Stages, stage)
	}
	if !drop.Verified {
		drop.RiskFlags = append(drop.RiskFlags, "OpenSea 未验证集合")
	}
	if !drop.Approved {
		drop.RiskFlags = append(drop.RiskFlags, "OpenSea 未标记为 approved")
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
		return nil, errors.New("金额不是有效的非负十进制数")
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	scaled := new(big.Rat).Mul(rational, new(big.Rat).SetInt(scale))
	if !scaled.IsInt() {
		return nil, fmt.Errorf("金额最多支持 %d 位小数", decimals)
	}
	return new(big.Int).Set(scaled.Num()), nil
}

func (a *App) CreateOpenSeaTasks(input OpenSeaTaskInput) ([]MintTask, error) {
	drop, err := a.InspectOpenSeaDrop(input.OpenSeaURL)
	if err != nil {
		return nil, err
	}
	var stage *OpenSeaStage
	for index := range drop.Stages {
		if drop.Stages[index].StageIndex == input.StageIndex {
			stage = &drop.Stages[index]
			break
		}
	}
	if stage == nil {
		return nil, errors.New("所选 OpenSea mint 阶段已不存在，请重新解析页面")
	}
	if !stage.AutoMintSupported {
		return nil, errors.New(stage.BlockReason)
	}
	if input.QuantityPerWallet == 0 {
		return nil, errors.New("每钱包 mint 数量必须大于 0")
	}
	if stage.MaxTotalMintableByWallet > 0 && input.QuantityPerWallet > stage.MaxTotalMintableByWallet {
		return nil, fmt.Errorf("每钱包数量超过 OpenSea 显示的上限 %d", stage.MaxTotalMintableByWallet)
	}
	if len(input.WalletAddresses) == 0 {
		return nil, errors.New("请至少选择一个本地钱包")
	}
	if input.PollIntervalSeconds < 2 {
		return nil, errors.New("轮询间隔不能小于 2 秒")
	}
	if strings.TrimSpace(input.RPCURL) == "" {
		input.RPCURL = defaultRobinhoodRPC
	}
	if _, err := url.ParseRequestURI(input.RPCURL); err != nil {
		return nil, errors.New("Robinhood RPC URL 无效")
	}
	maxGasPrice, err := decimalToWei(input.MaxGasPriceGwei, 9)
	if err != nil || maxGasPrice.Sign() <= 0 {
		return nil, errors.New("最大 gas price（Gwei）必须是正数")
	}
	maxTotalCost, err := decimalToWei(input.MaxTotalCostETH, 18)
	if err != nil || maxTotalCost.Sign() <= 0 {
		return nil, errors.New("单钱包最大总成本（ETH）必须是正数")
	}
	pageMintValue := new(big.Int).Mul(new(big.Int).SetUint64(input.QuantityPerWallet), mustBigInt(stage.PriceWei))
	if maxTotalCost.Cmp(pageMintValue) < 0 {
		return nil, errors.New("单钱包最大总成本低于 OpenSea 当前显示的 mint 价格")
	}
	seen := make(map[common.Address]bool)
	wallets := make([]common.Address, 0, len(input.WalletAddresses))
	for _, raw := range input.WalletAddresses {
		if !common.IsHexAddress(raw) {
			return nil, fmt.Errorf("钱包地址无效: %s", raw)
		}
		address := common.HexToAddress(raw)
		if seen[address] {
			continue
		}
		if !a.hasWallet(address) {
			return nil, fmt.Errorf("钱包 %s 尚未导入本地应用", address.Hex())
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
			ID: id, Name: fmt.Sprintf("%s · %s…%s", name, wallet.Hex()[:8], wallet.Hex()[38:]),
			OpenSeaURL: drop.URL, WalletAddress: wallet.Hex(),
			Network:  ChainConfig{Name: "Robinhood Chain", ChainID: robinhoodChainID, RPCURL: input.RPCURL},
			Contract: drop.Contract,
			SaleGate: SaleGate{Function: "opensea_seadrop_public", Expect: true, PollIntervalSeconds: input.PollIntervalSeconds},
			Mint:     MintConfig{Method: "SeaDrop.mintPublic", Quantity: input.QuantityPerWallet, ValueWei: pageMintValue.String(), MaxGasPriceWei: maxGasPrice.String(), MaxTotalCostWei: maxTotalCost.String()},
			OpenSea:  &OpenSeaTaskSnapshot{Slug: drop.Slug, Stage: *stage, Collection: drop.Name},
			Enabled:  true, Status: "ready",
		})
	}
	a.mu.Lock()
	for _, existing := range a.tasks {
		if !existing.Enabled || existing.OpenSea == nil || existing.OpenSea.Slug != drop.Slug || existing.OpenSea.Stage.StageIndex != stage.StageIndex {
			continue
		}
		for _, candidate := range created {
			if common.HexToAddress(existing.WalletAddress) == common.HexToAddress(candidate.WalletAddress) {
				a.mu.Unlock()
				return nil, fmt.Errorf("钱包 %s 已有同一 OpenSea 阶段的启用任务", candidate.WalletAddress)
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

var (
	seaDropABI = mustParseABI(`[
		{"type":"function","name":"getPublicDrop","stateMutability":"view","inputs":[{"name":"nftContract","type":"address"}],"outputs":[{"name":"","type":"tuple","components":[{"name":"mintPrice","type":"uint80"},{"name":"startTime","type":"uint48"},{"name":"endTime","type":"uint48"},{"name":"maxTotalMintableByWallet","type":"uint16"},{"name":"feeBps","type":"uint16"},{"name":"restrictFeeRecipients","type":"bool"}]}]},
		{"type":"function","name":"getAllowedFeeRecipients","stateMutability":"view","inputs":[{"name":"nftContract","type":"address"}],"outputs":[{"name":"","type":"address[]"}]},
		{"type":"function","name":"mintPublic","stateMutability":"payable","inputs":[{"name":"nftContract","type":"address"},{"name":"feeRecipient","type":"address"},{"name":"minterIfNotPayer","type":"address"},{"name":"quantity","type":"uint256"}],"outputs":[]}
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

func callContractABI(ctx context.Context, client *ethclient.Client, target common.Address, contractABI abi.ABI, method string, arguments ...any) ([]any, error) {
	data, err := contractABI.Pack(method, arguments...)
	if err != nil {
		return nil, err
	}
	result, err := client.CallContract(ctx, ethereum.CallMsg{To: &target, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return contractABI.Unpack(method, result)
}

func findSeaDrop(ctx context.Context, client *ethclient.Client, nft common.Address) (common.Address, error) {
	event := seaDropTokenABI.Events["AllowedSeaDropUpdated"]
	logs, err := client.FilterLogs(ctx, ethereum.FilterQuery{Addresses: []common.Address{nft}, Topics: [][]common.Hash{{event.ID}}})
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
	code, codeErr := client.CodeAt(ctx, fallback, nil)
	if codeErr == nil && len(code) > 0 {
		return fallback, nil
	}
	if err != nil {
		return common.Address{}, fmt.Errorf("无法读取 AllowedSeaDropUpdated: %w", err)
	}
	return common.Address{}, errors.New("无法从 NFT 合约发现 SeaDrop 地址")
}

func readPublicDrop(ctx context.Context, client *ethclient.Client, seaDrop, nft common.Address) (seaDropPublicConfig, error) {
	values, err := callContractABI(ctx, client, seaDrop, seaDropABI, "getPublicDrop", nft)
	if err != nil || len(values) != 1 {
		if err == nil {
			err = errors.New("SeaDrop 返回值数量错误")
		}
		return seaDropPublicConfig{}, err
	}
	converted := abi.ConvertType(values[0], new(seaDropPublicConfig))
	config, ok := converted.(*seaDropPublicConfig)
	if !ok {
		return seaDropPublicConfig{}, errors.New("无法解码 SeaDrop public drop")
	}
	return *config, nil
}

func readAllowedFeeRecipients(ctx context.Context, client *ethclient.Client, seaDrop, nft common.Address) ([]common.Address, error) {
	values, err := callContractABI(ctx, client, seaDrop, seaDropABI, "getAllowedFeeRecipients", nft)
	if err != nil || len(values) != 1 {
		return nil, err
	}
	recipients, ok := values[0].([]common.Address)
	if !ok {
		return nil, errors.New("无法解码 SeaDrop fee recipients")
	}
	return recipients, nil
}

func readMintStats(ctx context.Context, client *ethclient.Client, nft, minter common.Address) (*big.Int, *big.Int, *big.Int, error) {
	values, err := callContractABI(ctx, client, nft, seaDropTokenABI, "getMintStats", minter)
	if err != nil || len(values) != 3 {
		return nil, nil, nil, err
	}
	minted, ok1 := values[0].(*big.Int)
	total, ok2 := values[1].(*big.Int)
	maximum, ok3 := values[2].(*big.Int)
	if !ok1 || !ok2 || !ok3 {
		return nil, nil, nil, errors.New("无法解码 NFT mint stats")
	}
	return minted, total, maximum, nil
}

func (a *App) runOpenSeaTask(ctx context.Context, client *ethclient.Client, task MintTask, account accounts.Account, chainID *big.Int) {
	if task.Network.ChainID != robinhoodChainID || task.OpenSea.Stage.StageType != "PUBLIC_SALE" {
		a.recordTask(task.ID, "failed", false, "", "任务不是受支持的 Robinhood OpenSea 公售阶段")
		return
	}
	nft := common.HexToAddress(task.Contract)
	seaDrop, err := findSeaDrop(ctx, client, nft)
	if err != nil {
		a.recordTask(task.ID, "failed", false, "", err.Error())
		return
	}
	pageStart, startErr := time.Parse(time.RFC3339Nano, task.OpenSea.Stage.StartTime)
	pageEnd, endErr := time.Parse(time.RFC3339Nano, task.OpenSea.Stage.EndTime)
	if startErr != nil || endErr != nil {
		a.recordTask(task.ID, "failed", false, "", "OpenSea 阶段时间无效，请重新创建任务")
		return
	}
	interval := time.Duration(task.SaleGate.PollIntervalSeconds) * time.Second
	for {
		now := time.Now()
		if now.After(pageEnd) {
			a.recordTask(task.ID, "failed", false, "", "OpenSea 所选 mint 阶段已结束")
			return
		}
		config, configErr := readPublicDrop(ctx, client, seaDrop, nft)
		a.checkedTask(task.ID)
		if configErr == nil && config.StartTime != nil && config.EndTime != nil {
			chainStart := time.Unix(config.StartTime.Int64(), 0)
			chainEnd := time.Unix(config.EndTime.Int64(), 0)
			if chainStart.Equal(pageStart) && chainEnd.Equal(pageEnd) && !now.Before(chainStart) && now.Before(chainEnd) {
				hash, mintErr := a.broadcastOpenSeaPublic(ctx, client, account, task, chainID, seaDrop, nft, config)
				if mintErr != nil {
					a.recordTask(task.ID, "failed", false, "", mintErr.Error())
				} else {
					a.recordTask(task.ID, "broadcast", false, hash, "")
				}
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

func (a *App) broadcastOpenSeaPublic(ctx context.Context, client *ethclient.Client, account accounts.Account, task MintTask, chainID *big.Int, seaDrop, nft common.Address, config seaDropPublicConfig) (string, error) {
	state := a.nonces.state(task.Network.ChainID, account.Address)
	state.mu.Lock()
	defer state.mu.Unlock()

	quantity := task.Mint.Quantity
	minted, total, maximum, err := readMintStats(ctx, client, nft, account.Address)
	if err != nil {
		return "", fmt.Errorf("读取钱包 mint 额度失败: %w", err)
	}
	requested := new(big.Int).SetUint64(quantity)
	if new(big.Int).Add(new(big.Int).Set(minted), requested).Cmp(new(big.Int).SetUint64(uint64(config.MaxTotalMintableByWallet))) > 0 {
		return "", fmt.Errorf("该钱包已 mint %s，追加 %d 会超过链上限额 %d", minted, quantity, config.MaxTotalMintableByWallet)
	}
	if new(big.Int).Add(new(big.Int).Set(total), requested).Cmp(maximum) > 0 {
		return "", errors.New("NFT 剩余供应量不足")
	}
	recipients, err := readAllowedFeeRecipients(ctx, client, seaDrop, nft)
	if err != nil {
		return "", fmt.Errorf("读取 OpenSea fee recipient 失败: %w", err)
	}
	var feeRecipient common.Address
	for _, candidate := range recipients {
		if candidate != (common.Address{}) {
			feeRecipient = candidate
			break
		}
	}
	if feeRecipient == (common.Address{}) {
		return "", errors.New("SeaDrop 没有可验证的 fee recipient，拒绝猜测地址")
	}
	data, err := seaDropABI.Pack("mintPublic", nft, feeRecipient, account.Address, requested)
	if err != nil {
		return "", err
	}
	value := new(big.Int).Mul(new(big.Int).Set(config.MintPrice), requested)
	return a.broadcastPreparedLocked(ctx, client, account, task, chainID, seaDrop, data, value, state)
}

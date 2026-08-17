# MintDesk Robinhood

[![CI](https://github.com/bizipoopoo/MintDesk/actions/workflows/ci.yml/badge.svg)](https://github.com/bizipoopoo/MintDesk/actions/workflows/ci.yml)
[![GitHub Release](https://img.shields.io/github/v/release/bizipoopoo/MintDesk?display_name=tag)](https://github.com/bizipoopoo/MintDesk/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MintDesk is an open-source Wails desktop application and Go toolset for monitoring and executing explicitly armed NFT mint tasks on Robinhood Chain. It supports OpenSea drop inspection, encrypted local wallet imports, multiple wallet tasks, scheduled pre-mint watching, transaction simulation, cost caps, and nonce-safe concurrency.

> **Financial and key-safety warning:** NFT minting can lose funds. Use a dedicated hot wallet with a limited balance. Verify every collection, stage, contract interaction, chain, quantity, and cost limit yourself. Never paste a valuable primary wallet or commit private keys, recovery phrases, keystores, RPC credentials, or signing certificates.

## Downloads

[GitHub Releases](https://github.com/bizipoopoo/MintDesk/releases) publish these unsigned community builds for every `v*` release tag:

- `MintDeskRobinhood-macOS-universal.zip` for Apple Silicon and Intel Macs.
- `MintDeskRobinhood-Windows-x64-Setup.exe` for Windows x64.
- `MintDeskRobinhood-Windows-x64-portable.zip` for a portable Windows x64 executable.

The macOS build is ad-hoc signed but not Apple-notarized. The Windows build is not signed with a commercial code-signing certificate, so macOS Gatekeeper or Windows SmartScreen may show a warning. Verify release provenance and checksums before running downloaded wallet software.

## Command-line monitor

`mint-watch` watches an EVM NFT drop through an RPC endpoint. OpenSea is useful for finding a collection, but the sale state and mint transaction live on that collection's smart contract, so this tool treats the chain as the source of truth.

The program reads a no-argument boolean sale function such as `saleActive()` every few seconds. When the configured value is observed it sends an optional Discord-compatible webhook and prints the exact configured transaction. Broadcasting is disabled by default.

## Setup

```sh
cp config.example.json config.json
go mod tidy
go run ./cmd/mint-watch --config config.json --once
```

Fill `config.json` only from the project's official contract and mint documentation:

- `contract`: the verified mint contract address on the correct network.
- `sale_check.function`: a no-argument view function that returns `bool`, for example `publicSaleActive()` or `saleActive()`. It is contract-specific.
- `mint_calldata`: the complete ABI-encoded mint call, including its 4-byte selector. Obtain it from the verified contract ABI or a transaction simulation; do not guess it from an OpenSea listing.
- `mint_value_wei`: the exact total mint price in wei, including quantity but excluding gas.
- `max_gas_price_wei`: the maximum gas price you are willing to pay in wei. Required for `--execute`.
- `max_total_cost_wei`: the maximum `mint_value_wei + gas_limit * gas_price` in wei. Required for `--execute`.
- `rpc_url`: a private RPC endpoint appropriate to the chain. Public endpoints often rate-limit polling.

The optional webhook uses Discord's `{ "content": "..." }` request format. Leave it blank to print only to the terminal.

## Monitoring

Run continuously:

```sh
go run ./cmd/mint-watch --config config.json
```

For an initial read-only validation:

```sh
go run ./cmd/mint-watch --config config.json --once
```

## Broadcasting a mint transaction

Transaction broadcast is intentionally opt-in. The private key is never read from configuration or written to disk. Use a dedicated hot wallet funded only with the amount you are prepared to spend, and verify the transaction against the official drop before launching it.

```sh
export MINT_PRIVATE_KEY='0x...'
go run ./cmd/mint-watch --config config.json --execute --confirm-transaction
unset MINT_PRIVATE_KEY
```

`--execute` has no retry loop after a successful broadcast. A transaction being accepted by the RPC does not guarantee a mint: eligibility, supply, per-wallet limits, and contract conditions can still cause it to revert or remain pending. Review the chain explorer transaction after broadcast.

## Limits

There is no universal OpenSea mint ABI. Contracts can use allowlists, signatures, proof arguments, time windows, ERC-721A methods, or third-party mint providers. This starter handles the common `bool` sale gate and a pre-encoded transaction. For a specific collection, inspect its verified contract ABI and adapt the check/call before using `--execute`.

## Local mint task runner

`mint-task` is a task manager for one-shot automatic mints. It keeps imported keys in a password-encrypted geth keystore under `mint-data/keystore`; tasks only contain the public wallet address. It does not save a raw private key in JSON.

Initialize the local store, then import a dedicated hot wallet. Do not use a wallet that contains assets you are not prepared to expose to a mint contract.

```sh
go run ./cmd/mint-task init
export MINT_PRIVATE_KEY='0x...'
export MINT_KEYSTORE_PASSWORD='use-a-strong-local-password'
go run ./cmd/mint-task wallet import
unset MINT_PRIVATE_KEY
go run ./cmd/mint-task wallet list
```

Copy `task.example.json` to a separate local file and fill it only with details taken from the collection's official verified contract. `quantity: 100` works for the common payable `mint(uint256)` ABI. When the mint function needs allowlist proofs, signatures, multiple arguments, or any other non-standard field, set the verified complete ABI call in `raw_calldata` instead. The runner does not invent proofs or bypass eligibility checks.

```sh
go run ./cmd/mint-task task create --file task.json
go run ./cmd/mint-task task list
```

Armed execution is explicit and broadcasts only once after the sale check reports the expected boolean value:

```sh
export MINT_KEYSTORE_PASSWORD='use-a-strong-local-password'
go run ./cmd/mint-task run --armed
```

Each task checks the RPC network ID, simulates the transaction through gas estimation, enforces `max_gas_price_wei` and `max_total_cost_wei`, then signs from the encrypted local wallet. It disables the task after either a broadcast or a failed mint attempt, so re-arming requires `task enable --id <task-id>` after review.

## Desktop app

### 桌面版快速使用说明（KUJI 示例）

下面直接演示从导入专用钱包到创建公开 mint 任务的完整流程。更详细的安装、故障排查和安全清单见 [桌面版完整指南](docs/desktop-guide.md)。

> [!CAUTION]
> [KUJI](https://opensea.io/collection/kuji-723097858/overview) 只作为界面示例，不代表推荐或背书。截图采集于 2026-08-17（GMT+8）；当时该 collection 显示为 **Not verified by OpenSea**、未标记 approved，且没有可交叉核对的官方官网或 X 链接。项目页面和阶段可能变化，启动任务前必须重新核对项目身份、合约、链、时间、价格和限额。

#### 1. 下载并建立本地钱包保险库

从 [GitHub Releases](https://github.com/bizipoopoo/MintDesk/releases) 下载对应系统的版本。当前社区构建未经过 Apple notarization 或商业 Windows 代码签名；只有确认 Release 来源和校验信息后，才绕过 Gatekeeper 或 SmartScreen 提示。

打开 **Wallets**，选择一种方式添加钱包：

- **Private keys**：每行一个私钥，可批量导入。
- **Import phrase**：从已有助记词派生 1–20 个 EVM 地址。
- **Generate**：生成新的 24 词助记词，并按 `m/44'/60'/0'/0/i` 派生地址。

设置 **Keystore password** 后导入。私钥会写入本机加密 keystore，任务数据只保存公开地址。新生成的助记词只显示一次，请离线备份。仅使用余额受限的专用热钱包，不要导入主钱包。

#### 2. 检查 KUJI OpenSea 页面

打开 **Mint tasks**，粘贴：

```text
https://opensea.io/collection/kuji-723097858/overview
```

点击 **Inspect OpenSea**。应用会读取合约、Robinhood Chain、供应量、OpenSea 验证标记、阶段时间、价格和每钱包限额，这些值不能在任务表单中手工覆盖。

![MintDesk 检查 KUJI OpenSea 铸造阶段的脱敏截图](docs/images/mintdesk-kuji-inspection.jpg)

截图只包含公开项目信息，已裁掉本地钱包和 RPC 凭据。采集时解析结果为：

| 阶段 | 时间（GMT+8） | 价格 | 每钱包限额 | 桌面自动化 |
| --- | --- | --- | --- | --- |
| GTD | 2026-08-18 22:00–23:00 | 0.0011 ETH | 3 | 不支持 |
| WL | 2026-08-18 23:00–2026-08-19 00:00 | 0.0011 ETH | 2 | 不支持 |
| Public | 2026-08-19 00:00–01:00 | 0.0011 ETH | 5 | 支持 |

合约为 `0xBaeb2775D3a14E92264ea5f22Db96eba7766c6c9`，供应量为 2,500，Chain ID 为 `4663`。应用按电脑本地时区显示时间，请确认系统时钟和时区正确。

GTD/WL 需要与钱包绑定的 OpenSea 签名或 Merkle proof，公开页面没有这些参数，因此会显示但不能选择。当前桌面自动化只支持 OpenSea SeaDrop 的 **Public (`PUBLIC_SALE`)** 阶段，不会猜测 proof 或把 allowlist 任务改成 public mint。

#### 3. 创建 Public mint 任务

在检查结果下方完成配置：

1. **Select local wallets**：选择专用热钱包；每个钱包生成一个独立任务。
2. **Mint quantity per wallet**：填写每钱包数量，不得超过阶段限额；以前 mint 的数量也占用限额。
3. **Mint monitoring speed**：Extreme 100 ms、Fast 500 ms、Slow 2 秒或 Very slow 5 秒。
4. **Maximum fee per gas (Gwei)**：设置可接受的最高 gas fee。
5. **Maximum total cost per wallet (ETH)**：限制每钱包的 mint 价格加最坏情况 gas 总成本。
6. **Robinhood RPC**：填写可信的 Chain ID 4663 RPC；私人 URL 可能包含 API key，不要截图或提交到 GitHub。
7. 点击 **Create collection for N wallets**。

多个钱包共用 RPC 时会统一限速；更快的监控速度不会绕过服务商限流。出现超时或 `429` 时，降低速度或更换可靠端点。

#### 4. Arm、运行和停止

回到 **Overview**，确认 **Enabled tasks** 后：

1. 输入 keystore 密码。
2. 勾选 **I understand this may broadcast real transactions.**。
3. 点击 **Arm & start**。

未来任务先在本地等待，阶段开始前约一分钟才连接 RPC。应用必须保持运行，电脑必须保持唤醒和联网。广播前会重新核对 Chain ID、SeaDrop 价格和时间、钱包已 mint 数量、剩余供应量、fee recipient、余额、gas/总成本上限，并模拟交易。

每个任务最多广播一笔交易；广播后响应不明确时不会自动重发。最终结果以链上交易回执为准。需要中止时先在 **Overview** 点击 **Stop runner**，然后可在 **Mint tasks** 对 collection 使用 **Disable all**、**Enable all** 或 **Delete**；删除任务不会删除本地加密钱包。

The Wails desktop app is specialized for Robinhood Chain OpenSea drops. It supports importing a private key or deriving up to 20 Ethereum addresses from a BIP-39 recovery phrase along the standard `m/44'/60'/0'/0/i` path. Private material is immediately encrypted into the local keystore; it is not added to task JSON.

Creating a desktop task requires only an OpenSea collection/mint URL. The app parses the collection contract, Robinhood chain, supply, OpenSea verification flags, stages, time windows, displayed price, and per-wallet limits. The user then selects any number of imported wallets and a quantity per wallet. One task is created per wallet, so different wallets execute concurrently. A wallet/chain nonce coordinator serializes preparation, simulation, signing, and broadcast for the same wallet.

`PUBLIC_SALE` stages use the collection's on-chain `AllowedSeaDropUpdated` data and SeaDrop `mintPublic` interface. Immediately before broadcast the app verifies chain ID 4663, reads the on-chain public drop, checks its time window against the selected OpenSea stage, reads the wallet's already-minted count and remaining supply, obtains an allowed fee recipient, calculates value from the on-chain price, enforces the configured gas and total-cost caps, and simulates the transaction.

OpenSea `SIGNED_PRESALE` and Merkle allowlist stages are displayed but cannot currently be armed. Those stages require wallet-specific signature/proof data that is not present in public page HTML. The app fails closed instead of guessing it or silently sending a public-sale transaction.

```sh
npm --prefix frontend install
go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
wails build
```

The resulting app is [build/bin/MintDeskRobinhood.app](build/bin/MintDeskRobinhood.app). Use the explicit **Arm & start** control only after reviewing the parsed OpenSea stage and setting per-wallet cost caps. Armed OpenSea tasks remain on an in-process timer without opening an RPC connection, then begin polling one minute before the verified stage start. Each watcher exits as soon as its task broadcasts, fails, is stopped, or reaches the stage end. The app must remain running and the Mac must be awake near the mint window. The runner sends at most one transaction per task and never retries an ambiguous broadcast automatically.

## Continuous integration and releases

The `CI` workflow runs Go tests, builds the React frontend, and performs macOS Universal and Windows x64 desktop smoke builds on pushes and pull requests.

To publish a release, update the application version, commit it, and push a matching version tag:

```sh
git tag v0.2.2
git push origin v0.2.2
```

The `Release` workflow builds both desktop platforms and creates or updates the matching GitHub Release. It can also be started manually with a release tag from the GitHub Actions page.

## License

MintDesk is available under the [MIT License](LICENSE).

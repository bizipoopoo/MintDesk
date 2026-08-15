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

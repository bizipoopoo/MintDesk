# MintDesk Robinhood Desktop Guide

This guide uses the [KUJI OpenSea page](https://opensea.io/collection/kuji-723097858/overview) to demonstrate the complete workflow for inspecting a project, creating a public mint task, arming the runner, and stopping or removing tasks.

> [!CAUTION]
> KUJI is used only to demonstrate the interface. This is not a recommendation or endorsement by MintDesk, its maintainers, or this guide. The screenshot was captured on August 17, 2026 (GMT+8); project pages, contracts, and mint stages can change at any time. At capture time, KUJI was **Not verified by OpenSea** and had no public official website or X link that could be used to verify the team. Independently verify the project identity, official announcement, contract, chain, stage, price, and wallet limit before use.

## 1. Download and launch the app

Download the appropriate package from [GitHub Releases](https://github.com/bizipoopoo/MintDesk/releases):

- macOS: `MintDeskRobinhood-macOS-universal.zip` for Apple Silicon and Intel Macs.
- Windows: `MintDeskRobinhood-Windows-x64-Setup.exe` or the portable ZIP.

Community builds are currently ad-hoc signed but not Apple-notarized, and Windows builds do not have a commercial code-signing certificate. Confirm that the file came from this repository's Release page and verify any published checksums before running it. Only after verifying the source should you use macOS **Control-click → Open** or **Privacy & Security → Open Anyway**, or Windows SmartScreen **More info**.

You can also [build the desktop app from source](../README.md#desktop-app).

## 2. Create a local wallet vault

Open **Wallets** in the left sidebar. The desktop app supports three wallet workflows:

1. **Private keys**: paste one dedicated hot-wallet private key per line.
2. **Import phrase**: derive 1–20 EVM addresses from an existing recovery phrase.
3. **Generate**: create a new 24-word recovery phrase and derive 1–20 addresses at `m/44'/60'/0'/0/i`.

Enter a **Keystore password** and import or generate the wallets. Private keys are immediately written to the encrypted local keystore; task JSON contains only public wallet addresses. A newly generated recovery phrase is displayed once and is not persisted by MintDesk. Back it up offline before clearing it from the screen.

Wallet safety rules:

- Use only dedicated hot wallets with limited balances. Never import a primary wallet or a wallet holding long-term assets.
- Never screenshot, commit, or share private keys, recovery phrases, the keystore password, or an RPC URL containing an API key.
- A keystore password protects local signing material; it does not prove that an NFT project or contract is safe.

## 3. Inspect the KUJI OpenSea mint

Open **Mint tasks** and paste this URL into **OpenSea mint URL**:

```text
https://opensea.io/collection/kuji-723097858/overview
```

Click **Inspect OpenSea**. MintDesk reads the collection contract, chain, supply, OpenSea verification flags, stage schedule, displayed price, and per-wallet limits. These inspected values cannot be manually overridden in the task form.

![Sanitized MintDesk screenshot inspecting KUJI OpenSea mint stages](images/mintdesk-kuji-inspection.jpg)

The screenshot contains only public project information; local wallets and RPC credentials were cropped out. At capture time, the interface showed:

| Field | Inspected value |
| --- | --- |
| Contract | `0xBaeb2775D3a14E92264ea5f22Db96eba7766c6c9` |
| Chain | Robinhood Chain, Chain ID `4663` |
| Supply | `2,500` |
| GTD | August 18, 2026, 22:00–23:00 GMT+8; `0.0011 ETH`; maximum 3 per wallet |
| WL | August 18, 2026, 23:00–August 19, 00:00 GMT+8; `0.0011 ETH`; maximum 2 per wallet |
| Public | August 19, 2026, 00:00–01:00 GMT+8; `0.0011 ETH`; maximum 5 per wallet |
| OpenSea flags | Not verified and not marked approved |

The app displays times in the computer's local timezone. When working across timezones, compare the app with OpenSea and the project's official announcement, and confirm that the computer's clock and timezone are correct.

### Why GTD and WL cannot be selected

KUJI's GTD and WL stages are allowlist stages. They require a wallet-specific OpenSea signature or Merkle proof, and those private parameters are not available in the public page HTML. MintDesk displays the stages but disables automatic task creation for them. It will not guess a proof or silently convert an allowlist task into a public mint.

Desktop automation currently supports only OpenSea SeaDrop **Public (`PUBLIC_SALE`)** stages. In this example, only **Public stage** can be used to create an automatic task.

## 4. Create a Public mint task

After verifying the inspection result, complete the remaining fields:

1. **Select local wallets**: choose the dedicated hot wallets to use. MintDesk creates one execution task per wallet.
2. **Collection task name**: keep the inspected project name or enter a clear local label.
3. **Mint quantity per wallet**: enter the quantity for each wallet. It must not exceed the stage limit, and any earlier mints by that wallet count toward the limit.
4. **Mint monitoring speed**:
   - Extreme: 100 ms
   - Fast: 500 ms
   - Slow: 2 seconds
   - Very slow: 5 seconds
5. **Maximum fee per gas (Gwei)**: set the highest gas fee you are willing to accept.
6. **Maximum total cost per wallet (ETH)**: cap the mint price plus worst-case gas for each wallet. Set this deliberately and leave enough room for reasonable gas.
7. **Robinhood RPC**: use a trusted Robinhood Chain RPC endpoint. Public endpoints may rate-limit polling. Private RPC URLs may contain API keys, so never screenshot or commit them.
8. Click **Create collection for N wallets**.

Wallets sharing one RPC endpoint are rate-limited together. Choosing a faster monitoring speed does not bypass provider protection. If requests time out or return `429`, choose a slower speed or a more reliable endpoint.

## 5. Review and arm the runner

Return to **Overview**, verify the **Enabled tasks** count and task details, and then:

1. Enter the **Keystore password** used for the local wallet vault.
2. Select **I understand this may broadcast real transactions.**
3. Click **Arm & start**.

This gives enabled tasks permission to broadcast real transactions. Future tasks wait on a local timer and normally connect to the RPC one minute before the selected stage starts. The app must remain open, and the computer must stay awake and online around the mint window.

Immediately before broadcast, MintDesk rechecks:

- that the RPC Chain ID is `4663`;
- the on-chain SeaDrop public price and start/end times;
- the wallet's existing mint count and the collection's remaining supply;
- the allowed fee recipient, wallet balance, gas cap, and total-cost cap;
- whether the transaction passes gas estimation and simulation.

Each task broadcasts at most one transaction. An RPC timeout after broadcast can leave the result ambiguous, so MintDesk does not automatically resend the transaction. Broadcast does not guarantee a successful mint; verify the final transaction receipt on a Robinhood Chain explorer.

## 6. Stop, disable, or delete tasks

- While the runner is active, click **Stop runner** in **Overview** to stop the current watchers.
- After stopping the runner, use **Disable all** or **Enable all** for a collection in **Mint tasks**.
- **Delete** removes every wallet task in that collection but does not delete encrypted local wallets.
- A watcher stops after broadcast, failure, a manual stop, or the stage end. Review the transaction receipt and failure reason before enabling it again. Never rebroadcast only because the interface has not yet shown a receipt.

Collection enable, disable, and delete controls are locked while the runner is active. Stop the runner before changing a collection.

## 7. Troubleshooting

**Inspect OpenSea fails or no stages appear**

Confirm that the URL is an OpenSea collection or mint page and that OpenSea is reachable. Click **Refresh** and try again. If the page does not publish a complete stage, MintDesk will not infer one.

**A stage is visible but its radio button is disabled**

The stage probably requires a wallet-specific signature or Merkle proof. The current version automates only public SeaDrop stages.

**Create collection is disabled**

Select at least one imported local wallet and confirm that a supported Public stage is selected.

**The task does not connect to the RPC far in advance**

This is expected. Future tasks wait locally and connect about one minute before the stage starts.

**Nothing broadcasts when the stage begins**

Check that the app is still open, the computer is awake, the task is enabled, the runner is armed, the RPC is available, the wallet balance covers the worst-case total, and the gas and total-cost caps are not too low.

**OpenSea and the project's announcement disagree**

Do not arm the task. Cross-check the official project account, official website, OpenSea page, and on-chain contract. Follower counts, paid verification, reposts, and third-party promotion are not proof of authenticity.

## Pre-flight checklist

- [ ] I am using a dedicated hot wallet with a limited balance.
- [ ] I verified the project identity and official links through attributable primary sources.
- [ ] The OpenSea contract exactly matches the contract published by the official project.
- [ ] The chain is Robinhood Chain and the Chain ID is `4663`.
- [ ] The Public stage schedule, price, quantity, and per-wallet limit agree across sources.
- [ ] The computer clock and timezone are correct.
- [ ] The gas fee and per-wallet total-cost cap match my budget.
- [ ] The RPC source is trusted, and I have not shared or committed its API key.
- [ ] I understand that **Arm & start** may broadcast a real transaction and I am prepared to verify its on-chain receipt.

# Security policy

MintDesk signs real EVM transactions and stores imported keys in a local encrypted keystore. Treat every mint contract and RPC endpoint as untrusted.

## Reporting a vulnerability

Please report suspected vulnerabilities through a private GitHub security advisory instead of a public issue. Include the affected version, impact, reproduction steps, and any suggested mitigation. Do not include real private keys, recovery phrases, RPC credentials, or transaction-signing secrets.

## Operational guidance

- Use a dedicated hot wallet with a strictly limited balance.
- Verify the OpenSea collection, Robinhood Chain network, mint stage, cost caps, and wallet limits before arming a task.
- Never commit `config.json`, `.env`, `mint-data/`, keystore files, signing certificates, private keys, or recovery phrases.
- Release binaries are not notarized or code-signed with a commercial certificate unless the release notes explicitly say otherwise.

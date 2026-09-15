# Solidity sources

`TokenizedAsset.sol` and `Sale.sol` are the two contracts PLAN.md §5
calls for. `RecoveryGuard.sol` is PLAN.md §15.3's Safe Guard contract,
added for wallet-recovery Branch B (§15.9 Phase 2) - it has no
dependency on the other two and no OpenZeppelin imports (it declares its
own four-line `IERC165`). All three are compiled ahead of time (not at
runtime) and the resulting ABI + bytecode are checked into
`../artifacts/` and embedded into the Go binary via `go:embed` - see
`../contracts.go`. There is no build step in `go build`; these sources
are for reference and for reproducing or updating the artifacts.

## Toolchain

- solc `0.8.24` (via the `solc` npm package, i.e. solc-js)
- `@openzeppelin/contracts` `4.9.6`
- Optimizer enabled, 200 runs (the standard default most tooling uses)

## Reproducing the build

```sh
npm install solc@0.8.24 @openzeppelin/contracts@4.9.6
node - <<'EOF'
const fs = require("fs"), path = require("path"), solc = require("solc");
function findImports(p) {
  try { return { contents: fs.readFileSync(path.join("node_modules", p), "utf8") }; }
  catch (e) { return { error: "not found: " + p }; }
}
const sources = {};
for (const f of ["TokenizedAsset.sol", "Sale.sol", "RecoveryGuard.sol"]) {
  sources[f] = { content: fs.readFileSync(f, "utf8") };
}
const input = {
  language: "Solidity", sources,
  settings: { optimizer: { enabled: true, runs: 200 }, outputSelection: { "*": { "*": ["abi", "evm.bytecode.object"] } } },
};
const out = JSON.parse(solc.compile(JSON.stringify(input), { import: findImports }));
for (const name of ["TokenizedAsset", "Sale", "RecoveryGuard"]) {
  const c = out.contracts[name + ".sol"][name];
  fs.writeFileSync("../artifacts/" + name + ".abi.json", JSON.stringify(c.abi));
  fs.writeFileSync("../artifacts/" + name + ".bin", c.evm.bytecode.object);
}
EOF
```

## RecoveryGuard's Guard interfaceId, verified independently

`RecoveryGuard.supportsInterface` accepts `type(Guard).interfaceId` -
computed from the XOR of `checkTransaction`'s and
`checkAfterExecution`'s own 4-byte selectors, per Solidity's ERC-165
`interfaceId` rule for an interface with multiple functions. This was
cross-checked independently in Go (`crypto.Keccak256` over each
function's canonical signature string, XORed) rather than trusted on
sight, and matches Safe's own well-known real Guard interfaceId
(`0xe6d7a83a`) exactly - confirming `RecoveryGuard.sol`'s `Guard`
interface declaration is selector-for-selector identical to
safe-global/safe-contracts' real one at v1.4.1, even though it's
declared locally here (`operation` as `uint8` rather than importing
Safe's own `Enum.Operation` - ABI-equivalent, since Solidity
canonicalizes an enum parameter to `uint8` in a function signature
either way).

## Restricted-asset authorization (PLAN.md §22 - supersedes the original "plain ERC-20" design)

Every tokenized asset is a restricted/regulated security - `TokenizedAsset`
now carries its own on-chain allow-list (`isAuthorized`, `authorize`/
`deauthorize`, both `onlyOwner`) rather than being a plain unrestricted
ERC-20: every transfer (mint included) requires both sender and recipient
to already be authorized, and `mint` auto-authorizes its recipient. This
mirrors the original's Stellar design more faithfully than the earlier
"plain ERC-20, all compliance off-chain" version of this contract did -
Stellar enforced the identical policy on-chain via the issuer account's
`AUTH_REQUIRED` flag plus a per-holder trustline the issuer had to
explicitly authorize, with a subscription's own transaction bundling
that authorization inline. The KYC gate itself still lives in the
backend (`tokenization/services.authorizeHolder`, checked before a
purchase is built) - only the actual holding/transfer restriction moved
on-chain, since Base has no trustline-authorization primitive to lean on
the way Stellar did.

# Solidity sources

`TokenizedAsset.sol` and `Sale.sol` are the two contracts PLAN.md §5
calls for. They're compiled ahead of time (not at runtime) and the
resulting ABI + bytecode are checked into `../artifacts/` and embedded
into the Go binary via `go:embed` - see `../contracts.go`. There is no
build step in `go build`; these sources are for reference and for
reproducing or updating the artifacts.

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
for (const f of ["TokenizedAsset.sol", "Sale.sol"]) {
  sources[f] = { content: fs.readFileSync(f, "utf8") };
}
const input = {
  language: "Solidity", sources,
  settings: { optimizer: { enabled: true, runs: 200 }, outputSelection: { "*": { "*": ["abi", "evm.bytecode.object"] } } },
};
const out = JSON.parse(solc.compile(JSON.stringify(input), { import: findImports }));
for (const name of ["TokenizedAsset", "Sale"]) {
  const c = out.contracts[name + ".sol"][name];
  fs.writeFileSync("../artifacts/" + name + ".abi.json", JSON.stringify(c.abi));
  fs.writeFileSync("../artifacts/" + name + ".bin", c.evm.bytecode.object);
}
EOF
```

## Why plain ERC-20, not a compliance-aware standard

Per PLAN.md §11: the original's actual on-chain representation of a
tokenized asset was a plain Stellar asset code/issuer pair with no
on-chain transfer restrictions - every compliance/KYC gate was enforced
off-chain, in the backend, before it would ever build a transfer for a
restricted asset. `TokenizedAsset` keeps that same split rather than
adopting a standard like ERC-1400: the chain enforces supply and
ownership, the backend enforces who is allowed to end up holding it.

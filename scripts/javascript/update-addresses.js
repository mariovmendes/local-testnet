// Rewrites the hardcoded contract-address DEFAULTS in the stress-test
// scripts (worker.js, create-accounts.js) from the currently deployed
// contracts.json files, the same source frontend-env uses for frontend/.env.
// Run via `make scripts` from the local-testnet repo root whenever the L2
// contracts get redeployed — CREATE2 addresses can shift between deploys,
// and these scripts have no other way to notice.
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(__dirname, '..', '..')
const networksDir = path.join(repoRoot, '.localnet', 'networks')

function readContractAddresses(chainDir) {
  const file = path.join(networksDir, chainDir, 'contracts.json')
  if (!fs.existsSync(file)) {
    throw new Error(`${file} not found — has \`localnet l2\` deployed contracts yet?`)
  }
  const doc = JSON.parse(fs.readFileSync(file, 'utf8'))
  return doc.addresses
}

const addressesA = readContractAddresses('rollup-a')
const addressesB = readContractAddresses('rollup-b')

// field name in DEFAULTS -> replacement value
const replacements = {
  CHAIN_A_BRIDGE: addressesA.ComposeL2ToL2Bridge,
  CHAIN_B_BRIDGE: addressesB.ComposeL2ToL2Bridge,
  CHAIN_A_TOKEN: addressesA.MockL2ERC20,
  CHAIN_B_TOKEN: addressesB.MockL2ERC20,
  CHAIN_A_ETH_LIQUIDITY: addressesA.ComposeETHLiquidity,
  CHAIN_B_ETH_LIQUIDITY: addressesB.ComposeETHLiquidity,
}

for (const [key, value] of Object.entries(replacements)) {
  if (!value) {
    throw new Error(`Missing address for ${key} in deployed contracts.json`)
  }
}

const targetFiles = ['worker.js', 'create-accounts.js']

for (const fileName of targetFiles) {
  const filePath = path.join(__dirname, fileName)
  let content = fs.readFileSync(filePath, 'utf8')
  let changed = 0

  for (const [key, value] of Object.entries(replacements)) {
    const pattern = new RegExp(`(${key}:\\s*')0x[0-9a-fA-F]+(')`)
    if (!pattern.test(content)) {
      console.warn(`  ${fileName}: no ${key} field found, skipping`)
      continue
    }
    content = content.replace(pattern, `$1${value}$2`)
    changed++
  }

  fs.writeFileSync(filePath, content)
  console.log(`  ${fileName}: updated ${changed} address field(s)`)
}

console.log('\nDone. accounts.json (if present) still holds tokens/ETH minted against the')
console.log('previous deployment\'s addresses — regenerate accounts via create-accounts.js')
console.log('if the contracts were actually redeployed (not just re-read).')

// Safety audit for a stress run: proves (a) how many cross-chain transfers
// actually completed on-chain and (b) that no account lost tokens.
//
// Each stress account is minted INITIAL tokens on chain A only, and always
// bridges to ITSELF on chain B, so its balances alone reconstruct the outcome:
//
//   takenFromUser = INITIAL - balanceA      (escrowed and not refunded)
//   delivered     = balanceB                (started at 0)
//   stranded      = takenFromUser - delivered
//
// stranded > 0  => tokens left the user and were neither delivered nor
//                  refunded: a fund-loss bug.
// stranded < 0  => the user received more than they sent: a double-spend bug.
import { ethers } from 'ethers'
import fs from 'node:fs'
import { addresses, cetBalanceOf } from './cet.mjs'

const INITIAL = ethers.parseUnits('10000', 18)
const UNIT = ethers.parseUnits('1', 18)
const TOKEN_A = addresses('rollup-a').MockL2ERC20
const BRIDGE = addresses('rollup-a').ComposeL2ToL2Bridge
const ERC20 = ['function balanceOf(address) view returns (uint256)']

const accounts = JSON.parse(fs.readFileSync('accounts.json', 'utf8'))
const pa = new ethers.JsonRpcProvider('http://127.0.0.1:18545')
const pb = new ethers.JsonRpcProvider('http://127.0.0.1:28545')
const ta = new ethers.Contract(TOKEN_A, ERC20, pa)
const chainIdA = (await pa.getNetwork()).chainId
const { cet, balanceOf: cetBal } = await cetBalanceOf(pb, TOKEN_A, chainIdA)
const tb = { balanceOf: cetBal }

let totalTaken = 0n, totalDelivered = 0n, totalStranded = 0n, totalExcess = 0n
const strandedAccounts = [], excessAccounts = []

const CHUNK = 50
for (let i = 0; i < accounts.length; i += CHUNK) {
  const slice = accounts.slice(i, i + CHUNK)
  const res = await Promise.all(
    slice.map(async a => [a.address, await ta.balanceOf(a.address), await tb.balanceOf(a.address)])
  )
  for (const [addr, balA, balB] of res) {
    const taken = INITIAL - balA
    const delivered = balB
    const stranded = taken - delivered
    totalTaken += taken
    totalDelivered += delivered
    if (stranded > 0n) { totalStranded += stranded; strandedAccounts.push([addr, stranded]) }
    if (stranded < 0n) { totalExcess += -stranded; excessAccounts.push([addr, -stranded]) }
  }
  process.stderr.write(`\r  scanned ${Math.min(i + CHUNK, accounts.length)}/${accounts.length}`)
}
process.stderr.write('\n')

const fmt = v => ethers.formatUnits(v, 18)
const asTransfers = v => (v / UNIT).toString()

console.log('\n================ FUND SAFETY AUDIT ================')
console.log(`accounts audited          : ${accounts.length}`)
console.log(`CET on B                  : ${cet}`)
console.log(`tokens taken from users   : ${fmt(totalTaken)}  (${asTransfers(totalTaken)} transfers escrowed & not refunded)`)
console.log(`tokens delivered on B     : ${fmt(totalDelivered)}  (${asTransfers(totalDelivered)} transfers completed)`)
console.log('')
console.log(`STRANDED (user lost)      : ${fmt(totalStranded)}  across ${strandedAccounts.length} accounts`)
console.log(`EXCESS   (user gained)    : ${fmt(totalExcess)}  across ${excessAccounts.length} accounts`)

if (strandedAccounts.length) {
  console.log('\n  worst stranded accounts:')
  strandedAccounts.sort((x, y) => (y[1] > x[1] ? 1 : -1)).slice(0, 10)
    .forEach(([a, v]) => console.log(`    ${a}  ${fmt(v)}`))
}
if (excessAccounts.length) {
  console.log('\n  accounts with excess:')
  excessAccounts.slice(0, 10).forEach(([a, v]) => console.log(`    ${a}  ${fmt(v)}`))
}

// The bridge on A should be holding exactly the tokens taken from users that
// have not been refunded. Anything else means escrow accounting drifted.
const bridgeA = await ta.balanceOf(BRIDGE)
const bridgeB = await tb.balanceOf(BRIDGE)
console.log('\n  bridge escrow on A        :', fmt(bridgeA))
console.log('  bridge liquidity on B     :', fmt(bridgeB))
console.log(`  escrow matches taken?     : ${bridgeA === totalTaken ? 'YES' : `NO (diff ${fmt(bridgeA - totalTaken)})`}`)
console.log('===================================================')

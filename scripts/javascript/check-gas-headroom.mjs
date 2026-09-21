#!/usr/bin/env node
// Preflight: will any transaction in this run be refused for gas reasons?
//
// Two refusals are possible and they pull in opposite directions:
//
//   too low   "gas price is less than basefee"  maxFee < basefee at execution
//   too high  "lack of funds for max fee"       gasLimit x maxFee > balance
//
// maxFee is fixed by the signature but checked at *execution*, minutes later,
// so the safe band is
//
//   basefee_at_exec  <=  maxFee = M x basefee_at_sign + tip  <=  balance/gasLimit
//
// which is non-empty only while `balance / (gasLimit x basefee)` stays above
// the factor the basefee can grow by in that window. Both bounds are driven by
// the basefee, so the only structural guarantee is a basefee that does not
// climb: keep demand under the EIP-1559 target (gasLimit/elasticity) and it
// decays to minBaseFee instead. This script measures that rather than assuming
// it, and reports how far the current state is from each wall.
//
// Constants are parsed out of worker.js on purpose. Duplicating them is what
// caused the 2026-09-13 failure: the fee multiplier went to 8x in two places
// but the 3M receive gas limit was only right-sized in one.
import fs from 'node:fs'
import { ethers } from 'ethers'

const RPCS = {
    A: process.env.CHAIN_A_RPC ?? 'http://127.0.0.1:17545',
    B: process.env.CHAIN_B_RPC ?? 'http://127.0.0.1:27545',
}
const SAMPLE_BLOCKS = 50   // window for the basefee trend
const SAMPLE_ACCOUNTS = 25 // balances are near-identical; no need for all of them
const MIN_HEADROOM = 10    // refuse to start with less than 10x to the cliff

const src = fs.readFileSync(new URL('./worker.js', import.meta.url), 'utf8')
const num = (name) => {
    const m = src.match(new RegExp(`${name}\\s*=\\s*([0-9_]+)n?`))
    if (m) return BigInt(m[1].replaceAll('_', ''))
    const p = src.match(new RegExp(`${name}\\s*=\\s*ethers\\.parseUnits\\('([\\d.]+)',\\s*'(\\w+)'\\)`))
    if (p) return ethers.parseUnits(p[1], p[2])
    throw new Error(`could not parse ${name} out of worker.js`)
}

const M = num('FEE_CEILING_MULTIPLIER')
const TIP = num('DEFAULT_PRIORITY_FEE')
// The escrow is set by the largest limit signed against each chain.
const LIMITS = {
    A: { name: 'bridge', gas: num('DEFAULT_BRIDGE_GAS') },
    B: { name: 'receive', gas: num('DEFAULT_RECEIVE_GAS') },
}

const gwei = (v) => `${(Number(v) / 1e9).toFixed(3)} gwei`

// Highest basefee an account can still sign an M x ceiling against and afford.
const cliffBasefee = (balance, mult, gas) => balance / (mult * gas)

if (process.argv.includes('--self-test')) {
    const assert = (await import('node:assert/strict')).default
    // Replay the 2026-09-13 08:56:53 refusal exactly as op-rbuilder logged it:
    //   lack of funds (1735419315733386991) for max fee (1745245987128000000)
    const balance = 1735419315733386991n
    const maxFee = 1745245987128000000n
    const oldGas = 3_000_000n
    const cliff = cliffBasefee(balance, 8n, oldGas)
    assert.equal(cliff, 72309138155n) // 72.3 gwei — the wall that run hit
    // The basefee that produced the logged max fee was just above that cliff.
    const basefeeAtSigning = (maxFee / oldGas - 1_000_000_000n) / 8n
    assert.ok(basefeeAtSigning > cliff, 'refusal must sit above the cliff')
    assert.ok(oldGas * (8n * basefeeAtSigning + 1_000_000_000n) > balance, 'escrow must exceed balance')
    // The fix is not a smaller limit — shrinking receive to 700k rejected 894
    // of 894 XTs in simulation, because its cost is bimodal (p50 404k, max
    // 1.5M) and simulation needs more headroom than execution. The fix is a
    // basefee that stays at the floor, which makes even a 3M limit cheap.
    const flooredBasefee = 40_000_000n // ~0.04 gwei, what the 120M/2 target yields
    assert.ok(oldGas * (8n * flooredBasefee + 1_000_000_000n) < balance / 100n,
        'at a floored basefee a 3M limit must cost under 1% of balance')
    console.log('self-test ok: reproduces the 72.3 gwei cliff; a floored basefee makes 3M cheap')
    process.exit(0)
}

let failed = false

console.log(`fee ceiling = ${M}x basefee + ${gwei(TIP)} tip   (parsed from worker.js)`)

const accounts = JSON.parse(fs.readFileSync(new URL('./accounts.json', import.meta.url), 'utf8'))
    .slice(0, SAMPLE_ACCOUNTS)

for (const [chain, url] of Object.entries(RPCS)) {
    const provider = new ethers.JsonRpcProvider(url)
    const head = await provider.getBlock('latest')
    const from = Math.max(1, head.number - SAMPLE_BLOCKS)
    const past = await provider.getBlock(from)

    const base = head.baseFeePerGas ?? 0n
    const { gas, name } = LIMITS[chain]

    const balances = await Promise.all(accounts.map((a) => provider.getBalance(a.address)))
    const minBalance = balances.reduce((m, b) => (b < m ? b : m))

    // Largest basefee this account can still afford to sign M x against.
    const cliff = cliffBasefee(minBalance, M, gas)
    const headroom = base === 0n ? Infinity : Number(cliff) / Number(base)
    // Growth the ceiling absorbs before the *other* refusal starts biting.
    const trend = Number(base) / Math.max(1, Number(past.baseFeePerGas ?? 1n))

    console.log(`\nchain ${chain}  (${name} tx, gasLimit ${gas.toLocaleString()})`)
    console.log(`  basefee now        ${gwei(base)}   (${trend.toFixed(2)}x over last ${head.number - from} blocks)`)
    console.log(`  block fullness     ${(100 * Number(head.gasUsed) / Number(head.gasLimit)).toFixed(1)}% of ${Number(head.gasLimit).toLocaleString()}`)
    console.log(`  min balance        ${ethers.formatEther(minBalance)} ETH`)
    console.log(`  escrow per tx      ${ethers.formatEther(gas * (M * base + TIP))} ETH`)
    console.log(`  affordability cliff at basefee ${gwei(cliff)}  ->  headroom ${headroom === Infinity ? 'infinite' : headroom.toFixed(1) + 'x'}`)

    if (headroom < MIN_HEADROOM) {
        console.log(`  FAIL: under ${MIN_HEADROOM}x to the cliff — this run will hit "lack of funds for max fee"`)
        failed = true
    }
    if (trend > 1.5) {
        console.log(`  FAIL: basefee is climbing — demand is over the EIP-1559 target, so the cliff will be reached eventually`)
        failed = true
    }
    if (M * base + TIP < base) {
        console.log(`  FAIL: ceiling below current basefee — "gas price is less than basefee"`)
        failed = true
    }
}

console.log(failed ? '\nNOT SAFE TO RUN' : '\nsafe: basefee stable and escrow well under balance')
process.exit(failed ? 1 : 0)

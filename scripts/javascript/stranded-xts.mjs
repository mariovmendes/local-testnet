// Follow-up to verify-funds.mjs: names the XTs behind the stranded tokens.
//
// verify-funds says "N tokens left users and never arrived". This asks the
// chains which leg broke: for every account short on B, replay its submissions
// and pull the receipt for the source-side bridge tx and the dest-side receive
// tx. A leg with bridge=1 (escrow taken) and recv != 1 (never delivered) is
// exactly one stranded token, and the join gives its session/instance id so the
// publisher and sidecar logs can be grepped for the decision.
import { ethers } from 'ethers'
import fs from 'node:fs'
import { addresses, cetBalanceOf } from './cet.mjs'

const INITIAL = ethers.parseUnits('10000', 18)
const UNIT = ethers.parseUnits('1', 18)
const TOKEN_A = addresses('rollup-a').MockL2ERC20
const ERC20 = ['function balanceOf(address) view returns (uint256)']

const accounts = JSON.parse(fs.readFileSync('accounts.json', 'utf8'))
const pa = new ethers.JsonRpcProvider('http://127.0.0.1:18545')
const pb = new ethers.JsonRpcProvider('http://127.0.0.1:28545')
const ta = new ethers.Contract(TOKEN_A, ERC20, pa)
const tb = { balanceOf: (await cetBalanceOf(pb, TOKEN_A, (await pa.getNetwork()).chainId)).balanceOf }

// 1. which accounts are short, and by how many transfers
const short = new Map()
for (let i = 0; i < accounts.length; i += 50) {
  const res = await Promise.all(accounts.slice(i, i + 50).map(async a =>
    [a.address, await ta.balanceOf(a.address), await tb.balanceOf(a.address)]))
  for (const [addr, balA, balB] of res) {
    const d = (INITIAL - balA) - balB
    if (d > 0n) short.set(addr.toLowerCase(), d / UNIT)
  }
}
const expected = [...short.values()].reduce((a, b) => a + b, 0n)
console.error(`short accounts: ${short.size} (${expected} transfers)`)

// 2. their submissions
const subs = new Map()
for (const f of fs.readdirSync('.').filter(f => /^xt-submissions-w[0-4]\.jsonl$/.test(f)))
  for (const l of fs.readFileSync(f, 'utf8').split('\n')) {
    if (!l.trim()) continue
    const r = JSON.parse(l)
    const a = r.address.toLowerCase()
    if (!short.has(a)) continue
    if (!subs.has(a)) subs.set(a, [])
    subs.get(a).push(r)
  }

// 3. per-leg receipts. status 1 = mined ok, 0 = reverted, null = never mined
const st = async (p, h) => { const r = await p.getTransactionReceipt(h); return r ? r.status : null }
const flat = [...subs.values()].flat()
const rows = []
for (let i = 0; i < flat.length; i += 50) {
  const res = await Promise.all(flat.slice(i, i + 50).map(async r =>
    ({ ...r, bridge: await st(pa, r.bridge_tx), recv: await st(pb, r.receive_tx) })))
  rows.push(...res)
  process.stderr.write(`\r  scanned ${Math.min(i + 50, flat.length)}/${flat.length} legs`)
}
process.stderr.write('\n')

const leaked = rows.filter(r => r.bridge === 1 && r.recv !== 1)
const tally = {}
for (const r of rows) {
  const k = `bridge=${r.bridge} recv=${r.recv}`
  tally[k] = (tally[k] || 0) + 1
}
console.log('\n============ STRANDED XT TRACE ============')
console.log('legs examined                :', rows.length)
console.log('escrowed but not delivered   :', leaked.length)
console.log('expected from balances       :', expected.toString())
console.log('all legs by outcome          :', tally)
fs.writeFileSync('stranded-xts.json', JSON.stringify(leaked, null, 2))
console.log('\nwrote stranded-xts.json')
console.log('sample:', JSON.stringify(leaked.slice(0, 2), null, 2))

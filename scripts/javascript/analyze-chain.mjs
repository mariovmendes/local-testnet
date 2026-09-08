// Scans a block range on both rollups and classifies every transaction by
// selector, reporting success/revert counts per call type. Used to prove that
// saga compensation (sendAbort*/recvAbort*/removeInbox) actually lands on-chain
// and succeeds when XTs are aborted.
import { ethers } from 'ethers'

const HDR = '(uint256,uint256,address,address,uint256,string)'
const SIGS = [
  ['approve', 'approve(address,uint256)'],
  ['bridgeERC20To', 'bridgeERC20To(uint256,address,uint256,address,uint256)'],
  ['receiveTokens', `receiveTokens(${HDR})`],
  ['receiveETH', `receiveETH(${HDR})`],
  ['putInbox', 'putInbox(uint256,address,address,uint256,string,bytes)'],
  ['removeInbox', 'removeInbox(uint256,address,address,uint256,string,bytes)'],
  ['sendConfirm', `sendConfirm(${HDR})`],
  ['sendAbortToken', 'sendAbortToken(uint256,address,address,address,uint256,uint256)'],
  ['sendAbortETH', 'sendAbortETH(uint256,address,address,uint256,uint256)'],
  ['recvConfirmToken', `recvConfirmToken(${HDR})`],
  ['recvAbortToken', `recvAbortToken(${HDR})`],
  ['recvConfirmETH', `recvConfirmETH(${HDR})`],
  ['recvAbortETH', `recvAbortETH(${HDR})`],
]
const bySel = new Map()
for (const [name, sig] of SIGS) bySel.set(ethers.id(sig).slice(0, 10), name)

const [fromArg, toArg] = process.argv.slice(2)
const CHAINS = [['rollup-a', 'http://127.0.0.1:18545'], ['rollup-b', 'http://127.0.0.1:28545']]

for (const [name, url] of CHAINS) {
  const p = new ethers.JsonRpcProvider(url)
  const tip = await p.getBlockNumber()
  const from = fromArg ? parseInt(fromArg) : Math.max(0, tip - 200)
  const to = toArg ? Math.min(parseInt(toArg), tip) : tip
  const stats = new Map()   // name -> {ok, fail}
  let deposits = 0, coordFiller = 0, unknown = 0

  for (let n = from; n <= to; n++) {
    const blk = await p.getBlock(n, true)
    if (!blk) continue
    for (const tx of blk.prefetchedTransactions) {
      if (tx.from.toLowerCase() === '0xdeaddeaddeaddeaddeaddeaddeaddeaddead0001') { deposits++; continue }
      // 0-value self-transfer with empty data == coordinator nonce-gap filler
      if ((tx.data === '0x' || tx.data === '') && tx.to && tx.from.toLowerCase() === tx.to.toLowerCase()) { coordFiller++; continue }
      const sel = (tx.data || '0x').slice(0, 10)
      const fn = bySel.get(sel)
      if (!fn) { unknown++; continue }
      const rc = await p.getTransactionReceipt(tx.hash)
      const e = stats.get(fn) ?? { ok: 0, fail: 0 }
      rc && rc.status === 1 ? e.ok++ : e.fail++
      stats.set(fn, e)
    }
  }

  console.log(`\n===== ${name}  blocks ${from}..${to} =====`)
  const order = SIGS.map(s => s[0]).filter(n => stats.has(n))
  if (!order.length) console.log('  (no classified txs)')
  for (const fn of order) {
    const { ok, fail } = stats.get(fn)
    const tag = /Abort|removeInbox/.test(fn) ? '  <== COMPENSATION' : ''
    console.log(`  ${fn.padEnd(18)} ok=${String(ok).padEnd(6)} reverted=${String(fail).padEnd(6)}${tag}`)
  }
  console.log(`  ${'coordFillerTx'.padEnd(18)} ${coordFiller}   (nonce-gap fillers)`)
  console.log(`  ${'deposits'.padEnd(18)} ${deposits}   unknown=${unknown}`)
}

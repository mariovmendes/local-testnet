// For each reverted sendAbortToken on rollup-a, decode its params, recover the
// revert reason, and check whether the matching bridgeERC20To (same sessionId)
// actually executed — which decides whether escrowed funds are stuck.
import { ethers } from 'ethers'

const p = new ethers.JsonRpcProvider('http://127.0.0.1:18545')
const SEND_ABORT = 'sendAbortToken(uint256,address,address,address,uint256,uint256)'
const BRIDGE = 'bridgeERC20To(uint256,address,uint256,address,uint256)'
const selAbort = ethers.id(SEND_ABORT).slice(0, 10)
const selBridge = ethers.id(BRIDGE).slice(0, 10)
const abiCoder = ethers.AbiCoder.defaultAbiCoder()

const [from, to] = process.argv.slice(2).map(Number)
const aborts = [], bridges = new Map()

for (let n = from; n <= to; n++) {
  const blk = await p.getBlock(n, true)
  if (!blk) continue
  for (const tx of blk.prefetchedTransactions) {
    const sel = (tx.data || '0x').slice(0, 10)
    if (sel === selAbort) {
      const [chainDest, token, sender, receiver, amount, sessionId] =
        abiCoder.decode(['uint256', 'address', 'address', 'address', 'uint256', 'uint256'], '0x' + tx.data.slice(10))
      aborts.push({ tx, chainDest, token, sender, receiver, amount, sessionId, block: n })
    } else if (sel === selBridge) {
      const [chainDest, tokenSrc, amount, receiver, sessionId] =
        abiCoder.decode(['uint256', 'address', 'uint256', 'address', 'uint256'], '0x' + tx.data.slice(10))
      bridges.set(sessionId.toString(), { hash: tx.hash, from: tx.from, block: n })
    }
  }
}

console.log(`reverted-abort candidates: ${aborts.length}, bridge txs indexed: ${bridges.size}\n`)

let stuck = 0, noEscrow = 0
for (const a of aborts.slice(0, 6)) {
  const rc = await p.getTransactionReceipt(a.tx.hash)
  if (rc.status === 1) continue
  let reason = '(none)'
  try {
    await p.call({ to: a.tx.to, from: a.tx.from, data: a.tx.data, blockTag: a.block })
  } catch (e) {
    reason = e.reason ?? e.shortMessage ?? String(e).slice(0, 120)
  }
  const b = bridges.get(a.sessionId.toString())
  let bridgeStatus = 'NOT FOUND in range'
  if (b) {
    const brc = await p.getTransactionReceipt(b.hash)
    bridgeStatus = brc.status === 1 ? `EXECUTED OK (block ${b.block})` : `reverted (block ${b.block})`
  }
  if (b && bridgeStatus.startsWith('EXECUTED')) stuck++; else noEscrow++
  console.log(`session ${a.sessionId.toString().slice(0, 12)}...`)
  console.log(`  abort block ${a.block}  sender=${a.sender}  amount=${ethers.formatUnits(a.amount, 18)}`)
  console.log(`  revert reason : ${reason}`)
  console.log(`  matching bridge: ${bridgeStatus}\n`)
}
console.log(`sampled: escrow-existed(=funds at risk)=${stuck}  no-escrow(=harmless)=${noEscrow}`)

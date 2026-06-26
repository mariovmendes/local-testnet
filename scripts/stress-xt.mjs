#!/usr/bin/env node

import { ethers } from 'ethers'

// ---------------------------------------------------------------------------
// Defaults (mirrors frontend .env + vite.config.ts)
// ---------------------------------------------------------------------------
const DEFAULTS = {
  CHAIN_A_ID: 77777,
  CHAIN_B_ID: 88888,
  CHAIN_A_RPC: 'http://127.0.0.1:17545',
  CHAIN_B_RPC: 'http://127.0.0.1:27545',
  SIDECAR_A_URL: 'http://127.0.0.1:17090',
  PRIVATE_KEY: '0xb5685871a40a5c19507b67f858941d9d6bed5012c37eee572aff7356eb316174',
  CHAIN_A_BRIDGE: '0xa21afbba5c0dc05fbfbc534906862074344d2aa1',
  CHAIN_B_BRIDGE: '0xa21afbba5c0dc05fbfbc534906862074344d2aa1',
  CHAIN_A_TOKEN: '0x73af42789cdfa96d8cd46cdb2458821292f10b9f',
  CHAIN_B_TOKEN: '0x73af42789cdfa96d8cd46cdb2458821292f10b9f',
  CHAIN_A_ETH_LIQUIDITY: '0x0382bc1c7e9df089adf6b1335d015e28d1f54b69',
  CHAIN_B_ETH_LIQUIDITY: '0x0382bc1c7e9df089adf6b1335d015e28d1f54b69',
}

// ---------------------------------------------------------------------------
// ABI fragments (same as frontend rollup.ts)
// ---------------------------------------------------------------------------
const ERC20_ABI = [
  'function balanceOf(address owner) view returns (uint256)',
  'function approve(address spender, uint256 amount) returns (bool)',
]

const BRIDGE_ABI = [
  'function bridgeERC20To(uint256 chainDest, address tokenSrc, uint256 amount, address receiver, uint256 sessionId)',
  'function bridgeEthTo(uint256 sessionId, uint256 chainDest, address receiver) payable',
  'function receiveTokens(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (address token, uint256 amount)',
  'function receiveETH(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (uint256 amount)',
]

const ETH_LIQUIDITY_ABI = ['function fund() payable']

// ---------------------------------------------------------------------------
// Gas defaults (frontend rollup.ts)
// ---------------------------------------------------------------------------
const DEFAULT_MAX_FEE = ethers.parseUnits('20', 'gwei')
const DEFAULT_PRIORITY_FEE = ethers.parseUnits('1', 'gwei')
const DEFAULT_RECEIVE_GAS = 3_000_000n

// ---------------------------------------------------------------------------
// CLI arg parsing
// ---------------------------------------------------------------------------
function parseArgs() {
  const a = process.argv.slice(2)
  const p = { ...DEFAULTS, asset: 'erc20', direction: 'a_to_b', count: 10, concurrency: 4, amount: '0.001' }

  for (let i = 0; i < a.length; i++) {
    const v = () => a[++i]
    switch (a[i]) {
      case '--asset':       p.asset = v(); break
      case '--direction':   p.direction = v(); break
      case '--count':       p.count = parseInt(v(), 10); break
      case '--concurrency': p.concurrency = parseInt(v(), 10); break
      case '--amount':      p.amount = v(); break
      case '--rpc-a':       p.CHAIN_A_RPC = v(); break
      case '--rpc-b':       p.CHAIN_B_RPC = v(); break
      case '--sidecar':     p.SIDECAR_A_URL = v(); break
      case '--pk':          p.PRIVATE_KEY = v(); break
      case '--help': case '-h': printHelp(); process.exit(0)
      default: console.error(`Unknown: ${a[i]}`); process.exit(1)
    }
  }

  if (!['erc20', 'eth'].includes(p.asset)) {
    console.error('--asset must be erc20 or eth'); process.exit(1)
  }
  if (!['a_to_b', 'b_to_a'].includes(p.direction)) {
    console.error('--direction must be a_to_b or b_to_a'); process.exit(1)
  }

  return p
}

function printHelp() {
  console.log(`
Usage: node scripts/stress-xt.mjs [options]

Generate maximum cross-chain transactions (XTs) with parallelism.

Options:
  --asset <erc20|eth>           Token type to bridge (default: erc20)
  --direction <a_to_b|b_to_a>   Bridge direction  (default: a_to_b)
  --count <number>              Total XTs to send  (default: 10)
  --concurrency <number>        Parallel workers   (default: 4)
  --amount <string>             Amount per XT      (default: 0.001)
  --rpc-a <url>                 Chain-A RPC URL
  --rpc-b <url>                 Chain-B RPC URL
  --sidecar <url>               Sidecar API URL
  --pk <key>                    Wallet private key (0x-prefixed)
  -h, --help                    Show this message

Examples:
  node scripts/stress-xt.mjs --asset eth --direction a_to_b --count 50 --concurrency 8 --amount 0.01
  node scripts/stress-xt.mjs --asset erc20 --direction b_to_a --count 100 --concurrency 16
`)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------
function generateSessionId() {
  const b = new Uint8Array(32)
  crypto.getRandomValues(b)
  return '0x' + Array.from(b, x => x.toString(16).padStart(2, '0')).join('')
}

function statusBar(w, total, done, ok, fail, err) {
  const p = total > 0 ? Math.round((done / total) * 100) : 0
  const bar = '[' + '='.repeat(Math.round(w * p / 100)).padEnd(w, ' ') + ']'
  return `${bar} ${done}/${total}  ok:${ok}  fail:${fail}  err:${err}`
}

// ---------------------------------------------------------------------------
// Sign a transaction (mimics signContractTx in rollup.ts)
// ---------------------------------------------------------------------------
async function signTx(signer, txReq, chainId, nonce) {
  const provider = signer.provider
  const fee = await provider.getFeeData()
  const mpf = fee.maxPriorityFeePerGas ?? DEFAULT_PRIORITY_FEE
  const mf = fee.maxFeePerGas ?? DEFAULT_MAX_FEE
  return signer.signTransaction({
    ...txReq,
    chainId: BigInt(chainId),
    nonce,
    type: 2,
    maxFeePerGas: mf,
    maxPriorityFeePerGas: mpf,
    from: await signer.getAddress(),
  })
}

// ---------------------------------------------------------------------------
// Transaction builders (from rollup.ts logic)
// ---------------------------------------------------------------------------
async function buildApprove(tokenAddr, spender, amount, signer, chainId, nonce) {
  const c = new ethers.Contract(tokenAddr, ERC20_ABI, signer)
  const tx = await c.approve.populateTransaction(spender, amount)
  return signTx(signer, { ...tx, gasLimit: 900000n }, chainId, nonce)
}

async function buildBridgeERC20(bridgeAddr, chainDest, tokenAddr, amount, receiver, sessionId, signer, chainId, nonce) {
  const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
  const tx = await c.bridgeERC20To.populateTransaction(chainDest, tokenAddr, amount, receiver, sessionId)
  return signTx(signer, { ...tx, gasLimit: 900000n }, chainId, nonce)
}

async function buildBridgeEth(bridgeAddr, chainDest, receiver, sessionId, value, signer, chainId, nonce) {
  const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
  const tx = await c.bridgeEthTo.populateTransaction(sessionId, chainDest, receiver, { value })
  return signTx(signer, { ...tx, gasLimit: 900000n }, chainId, nonce)
}

async function buildReceiveTokens(bridgeAddr, sessionId, chainSrc, chainDest, bridgeSrc, receiver, signer, chainId, nonce) {
  const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
  const tx = await c.receiveTokens.populateTransaction({
    chainSrc, chainDest, sender: bridgeSrc, receiver, sessionId, label: 'SEND_TOKENS',
  })
  return signTx(signer, { ...tx, gasLimit: DEFAULT_RECEIVE_GAS }, chainId, nonce)
}

async function buildReceiveEth(bridgeAddr, sessionId, chainSrc, chainDest, bridgeSrc, receiver, signer, chainId, nonce) {
  const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
  const tx = await c.receiveETH.populateTransaction({
    chainSrc, chainDest, sender: bridgeSrc, receiver, sessionId, label: 'SEND_ETH',
  })
  return signTx(signer, { ...tx, gasLimit: DEFAULT_RECEIVE_GAS }, chainId, nonce)
}

// ---------------------------------------------------------------------------
// Sidecar API
// ---------------------------------------------------------------------------
async function submitXT(sidecarUrl, transactions) {
  const txs = {}
  for (const [k, v] of Object.entries(transactions)) txs[k] = v
  const res = await fetch(`${sidecarUrl}/xt`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ transactions: txs }),
  })
  if (!res.ok) throw new Error(`submitXT ${res.status}: ${await res.text()}`)
  return res.json()
}

async function getXTStatus(sidecarUrl, instanceId) {
  const res = await fetch(`${sidecarUrl}/xt/${instanceId}`)
  if (!res.ok) throw new Error(`getXTStatus ${res.status}`)
  return res.json()
}

async function waitForDecision(sidecarUrl, instanceId, timeoutMs = 60000) {
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    try {
      const s = await getXTStatus(sidecarUrl, instanceId)
      if (s.decision !== undefined) return s.decision
      if (s.status === 'committed') return true
      if (s.status === 'aborted') return false
    } catch {}
    await new Promise(r => setTimeout(r, 300))
  }
  throw new Error('Timeout waiting for decision')
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
async function main() {
  const cfg = parseArgs()

  console.log(`\n  ⚡ Stress XT Generator`)
  console.log(`  ─────────────────────`)
  console.log(`  asset:      ${cfg.asset}`)
  console.log(`  direction:  ${cfg.direction}`)
  console.log(`  count:      ${cfg.count}`)
  console.log(`  concurrency: ${cfg.concurrency}`)
  console.log(`  amount:     ${cfg.amount}`)
  console.log(`  rpc-a:      ${cfg.CHAIN_A_RPC}`)
  console.log(`  rpc-b:      ${cfg.CHAIN_B_RPC}`)
  console.log(`  sidecar:    ${cfg.SIDECAR_A_URL}\n`)

  // -----------------------------------------------------------------------
  // Providers & signers
  // -----------------------------------------------------------------------
  const providerA = new ethers.JsonRpcProvider(cfg.CHAIN_A_RPC)
  const providerB = new ethers.JsonRpcProvider(cfg.CHAIN_B_RPC)
  const signerA = new ethers.Wallet(cfg.PRIVATE_KEY, providerA)
  const signerB = new ethers.Wallet(cfg.PRIVATE_KEY, providerB)
  const senderA = await signerA.getAddress()
  const senderB = await signerB.getAddress()

  const { asset, direction, count, concurrency } = cfg
  const parsedAmount = ethers.parseUnits(cfg.amount, 18)
  const isERC20 = asset === 'erc20'
  const isAToB = direction === 'a_to_b'

  const sourceChainId = isAToB ? cfg.CHAIN_A_ID : cfg.CHAIN_B_ID
  const destChainId = isAToB ? cfg.CHAIN_B_ID : cfg.CHAIN_A_ID

  const tokenSource = isAToB ? cfg.CHAIN_A_TOKEN : cfg.CHAIN_B_TOKEN
  const bridgeSource = isAToB ? cfg.CHAIN_A_BRIDGE : cfg.CHAIN_B_BRIDGE
  const bridgeDest = isAToB ? cfg.CHAIN_B_BRIDGE : cfg.CHAIN_A_BRIDGE
  const signerSource = isAToB ? signerA : signerB
  const signerDest = isAToB ? signerB : signerA
  const receiverDest = isAToB ? senderB : senderA
  const providerSource = isAToB ? providerA : providerB
  const providerDest = isAToB ? providerB : providerA

  // -----------------------------------------------------------------------
  // Nonce strides per XT (same as frontend BridgeForm.tsx line 270)
  //   ERC20 source: approve + bridge = 2 nonces; dest: receive = 1
  //   ETH source: bridge = 1; dest: receive = 1
  // -----------------------------------------------------------------------
  const sourceStride = isERC20 ? 2 : 1
  const destStride = 1

  // -----------------------------------------------------------------------
  // Check ETH liquidity if needed
  // -----------------------------------------------------------------------
  if (asset === 'eth') {
    const liqAddr = isAToB ? cfg.CHAIN_B_ETH_LIQUIDITY : cfg.CHAIN_A_ETH_LIQUIDITY
    const destChain = isAToB ? 'B' : 'A'
    const balance = await providerDest.getBalance(liqAddr)
    const needed = parsedAmount * BigInt(count)
    if (balance < needed) {
      console.error(`  ETH liquidity pool ${destChain} has ${ethers.formatEther(balance)} ETH, needs ${ethers.formatEther(needed)} ETH.`)
      console.error(`  Seed it first: run "node scripts/seed-liquidity.mjs --amount ${cfg.amount}"`)
      process.exit(1)
    }
  }

  // -----------------------------------------------------------------------
  // Get base nonces from both chains
  // -----------------------------------------------------------------------
  const baseNonceA = await providerA.getTransactionCount(senderA, 'pending')
  const baseNonceB = await providerB.getTransactionCount(senderB, 'pending')

  // Nonce counters (shared atomically across workers — no await between R&W)
  let nextNonceA = baseNonceA
  let nextNonceB = baseNonceB

  function allocateNonces() {
    // MUST be called synchronously (no await) to stay atomic in single-threaded JS
    const na = nextNonceA
    const nb = nextNonceB
    if (isAToB) {
      nextNonceA += sourceStride
      nextNonceB += destStride
    } else {
      nextNonceA += destStride
      nextNonceB += sourceStride
    }
    return { na, nb }
  }

  // -----------------------------------------------------------------------
  // Results tracking
  // -----------------------------------------------------------------------
  const results = { submitted: 0, committed: 0, aborted: 0, errors: 0, started: 0 }
  const startTime = Date.now()

  function printProgress() {
    const done = results.submitted + results.errors
    const w = process.stdout.columns ? Math.min(process.stdout.columns - 30, 40) : 30
    const line = statusBar(w, count, done, results.committed, results.aborted, results.errors)
    const elapsed = ((Date.now() - startTime) / 1000).toFixed(1)
    process.stdout.write('\r' + line + `  ${elapsed}s` + ' '.repeat(4))
  }

  // -----------------------------------------------------------------------
  // Worker: builds, submits, waits for sidecar decision, then verifies
  // on-chain receipts so rollup state is actually updated.
  // -----------------------------------------------------------------------
  async function submitOneXT() {
    const { na, nb } = allocateNonces()
    const sessionId = generateSessionId()

    // Build signed tx bytes
    const txs = {}
    let txSourceBytes, txDestBytes

    if (isERC20) {
      if (isAToB) {
        const approve = await buildApprove(tokenSource, bridgeSource, parsedAmount, signerSource, sourceChainId, na)
        const bridge = await buildBridgeERC20(bridgeSource, destChainId, tokenSource, parsedAmount, receiverDest, sessionId, signerSource, sourceChainId, na + 1)
        const recv = await buildReceiveTokens(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nb)
        txs[sourceChainId] = [approve, bridge]
        txs[destChainId] = [recv]
        txSourceBytes = bridge
        txDestBytes = recv
      } else {
        const approve = await buildApprove(tokenSource, bridgeSource, parsedAmount, signerSource, sourceChainId, nb)
        const bridge = await buildBridgeERC20(bridgeSource, destChainId, tokenSource, parsedAmount, receiverDest, sessionId, signerSource, sourceChainId, nb + 1)
        const recv = await buildReceiveTokens(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, na)
        txs[sourceChainId] = [approve, bridge]
        txs[destChainId] = [recv]
        txSourceBytes = bridge
        txDestBytes = recv
      }
    } else {
      if (isAToB) {
        const bridge = await buildBridgeEth(bridgeSource, destChainId, receiverDest, sessionId, parsedAmount, signerSource, sourceChainId, na)
        const recv = await buildReceiveEth(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nb)
        txs[sourceChainId] = [bridge]
        txs[destChainId] = [recv]
        txSourceBytes = bridge
        txDestBytes = recv
      } else {
        const bridge = await buildBridgeEth(bridgeSource, destChainId, receiverDest, sessionId, parsedAmount, signerSource, sourceChainId, nb)
        const recv = await buildReceiveEth(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, na)
        txs[sourceChainId] = [bridge]
        txs[destChainId] = [recv]
        txSourceBytes = bridge
        txDestBytes = recv
      }
    }

    // Compute on-chain tx hashes from the signed bytes (deterministic)
    const txHashSource = ethers.Transaction.from(txSourceBytes).hash
    const txHashDest = ethers.Transaction.from(txDestBytes).hash

    // Submit to sidecar
    const resp = await submitXT(cfg.SIDECAR_A_URL, txs)

    // Wait for sidecar 2PC decision
    const ok = await waitForDecision(cfg.SIDECAR_A_URL, resp.instance_id)

    if (!ok) {
      results.aborted++
      results.submitted++
      return
    }

    // Sidecar committed — now wait for on-chain receipts on each rollup
    try {
      const [receiptSource, receiptDest] = await Promise.all([
        providerSource.waitForTransaction(txHashSource, 1, 30000),
        providerDest.waitForTransaction(txHashDest, 1, 30000),
      ])

      if (receiptSource?.status === 1 && receiptDest?.status === 1) {
        results.committed++
      } else {
        results.aborted++
      }
    } catch {
      // Receipt polling timed out or failed — the XT was committed by the
      // sidecar but the on-chain txs haven't confirmed yet.
      results.errors++
    }
    results.submitted++
  }

  // -----------------------------------------------------------------------
  // Worker pool
  // -----------------------------------------------------------------------
  async function runWorker() {
    while (true) {
      const idx = results.started++
      if (idx >= count) break
      try {
        await submitOneXT()
      } catch (e) {
        results.errors++
        results.submitted++
        console.error(`\n  Worker error on XT #${idx}: ${e.message}`)
      }
    }
  }

  // -----------------------------------------------------------------------
  // Launch workers
  // -----------------------------------------------------------------------
  const poolSize = Math.min(concurrency, count)
  const workers = Array.from({ length: poolSize }, (_, i) => runWorker())

  // Live progress display
  const interval = setInterval(() => { printProgress() }, 200)

  await Promise.all(workers)
  clearInterval(interval)
  printProgress()
  console.log('')

  // -----------------------------------------------------------------------
  // Summary
  // -----------------------------------------------------------------------
  const elapsed = (Date.now() - startTime) / 1000
  const pct = count > 0 ? Math.round((results.committed / count) * 100) : 0
  const tps = elapsed > 0 ? (results.committed / elapsed).toFixed(1) : '-'
  console.log(`\n  ───────────────────────────────────────`)
  console.log(`  ${results.submitted} submitted · ${results.committed} committed · ${results.aborted} aborted · ${results.errors} errors`)
  console.log(`  ${elapsed.toFixed(1)}s elapsed · ${tps} XTs/s · success rate ${pct}%\n`)
}

main().catch(e => { console.error(e); process.exit(1) })

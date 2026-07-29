#!/usr/bin/env node

import { ethers } from 'ethers'

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

const ERC20_ABI = [
  'function approve(address spender, uint256 amount) returns (bool)',
]

const BRIDGE_ABI = [
  'function bridgeERC20To(uint256 chainDest, address tokenSrc, uint256 amount, address receiver, uint256 sessionId)',
  'function bridgeEthTo(uint256 sessionId, uint256 chainDest, address receiver) payable',
  'function receiveTokens(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (address token, uint256 amount)',
  'function receiveETH(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (uint256 amount)',
]

const DEFAULT_MAX_FEE = ethers.parseUnits('20', 'gwei')
const DEFAULT_PRIORITY_FEE = ethers.parseUnits('1', 'gwei')
const DEFAULT_RECEIVE_GAS = 3_000_000n

function parseArgs() {
  const a = process.argv.slice(2)
  const p = { ...DEFAULTS, asset: 'erc20', direction: 'a_to_b', interval: 10, amount: '0.001' }

  for (let i = 0; i < a.length; i++) {
    const v = () => a[++i]
    switch (a[i]) {
      case '--asset':     p.asset = v(); break
      case '--direction': p.direction = v(); break
      case '--interval':  p.interval = parseInt(v(), 10); break
      case '--amount':    p.amount = v(); break
      case '--rpc-a':     p.CHAIN_A_RPC = v(); break
      case '--rpc-b':     p.CHAIN_B_RPC = v(); break
      case '--sidecar':   p.SIDECAR_A_URL = v(); break
      case '--pk':        p.PRIVATE_KEY = v(); break
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
Usage: node scripts/infinite-stress.mjs [options]

Send XTs in an infinite loop at a fixed interval (fire-and-forget).
Press Ctrl+C to stop and print stats.

Options:
  --asset <erc20|eth>         Token type to bridge (default: erc20)
  --direction <a_to_b|b_to_a> Bridge direction     (default: a_to_b)
  --interval <ms>             Interval between XTs  (default: 10)
  --amount <string>           Amount per XT        (default: 0.001)
  --rpc-a <url>               Chain-A RPC URL
  --rpc-b <url>               Chain-B RPC URL
  --sidecar <url>             Sidecar API URL
  --pk <key>                  Wallet private key (0x-prefixed)
  -h, --help                  Show this message

Examples:
  node scripts/infinite-stress.mjs --interval 10
  node scripts/infinite-stress.mjs --asset eth --interval 50 --amount 0.01
`)
}

function generateSessionId() {
  const b = new Uint8Array(32)
  crypto.getRandomValues(b)
  return '0x' + Array.from(b, x => x.toString(16).padStart(2, '0')).join('')
}

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

async function submitXT(sidecarUrl, transactions) {
  const txs = {}
  for (const [k, v] of Object.entries(transactions)) txs[k] = v
  const res = await fetch(`${sidecarUrl}/xt`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ transactions: txs }),
  })
  if (!res.ok) {
    const body = await res.text()
    throw new Error(`submitXT ${res.status}: ${body}`)
  }
  return res.json()
}

async function main() {
  const cfg = parseArgs()

  const providerA = new ethers.JsonRpcProvider(cfg.CHAIN_A_RPC)
  const providerB = new ethers.JsonRpcProvider(cfg.CHAIN_B_RPC)
  const signerA = new ethers.Wallet(cfg.PRIVATE_KEY, providerA)
  const signerB = new ethers.Wallet(cfg.PRIVATE_KEY, providerB)
  const senderA = await signerA.getAddress()
  const senderB = await signerB.getAddress()

  const parsedAmount = ethers.parseUnits(cfg.amount, 18)
  const isERC20 = cfg.asset === 'erc20'
  const isAToB = cfg.direction === 'a_to_b'

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

  const sourceStride = isERC20 ? 2 : 1

  // Check ETH liquidity pool if needed
  if (cfg.asset === 'eth') {
    const liqAddr = isAToB ? cfg.CHAIN_B_ETH_LIQUIDITY : cfg.CHAIN_A_ETH_LIQUIDITY
    const destChain = isAToB ? 'B' : 'A'
    const balance = await providerDest.getBalance(liqAddr)
    const needed = parsedAmount * 100n
    if (balance < needed) {
      console.error(`\n  ETH liquidity pool ${destChain} has ${ethers.formatEther(balance)} ETH.`)
      console.error(`  Seed more: node scripts/seed-liquidity.mjs --amount ${cfg.amount}\n`)
    }
  }

  let nonceSource = await providerSource.getTransactionCount(await signerSource.getAddress(), 'pending')
  let nonceDest = await providerDest.getTransactionCount(await signerDest.getAddress(), 'pending')

  const startTime = Date.now()
  let submitted = 0
  let failed = 0
  let conflicts = 0
  let running = true

  process.on('SIGINT', () => {
    running = false
    setTimeout(() => {
      const elapsed = ((Date.now() - startTime) / 1000).toFixed(1)
      const rate = elapsed > 0 ? (submitted / parseFloat(elapsed)).toFixed(1) : '0'
      console.log(`\n\n  ────────────────────────────────────`)
      console.log(`  submitted: ${submitted}`)
      console.log(`  failed:    ${failed}`)
      console.log(`  conflicts: ${conflicts}`)
      console.log(`  ${elapsed}s · ${rate} tx/s`)
      console.log(`  ────────────────────────────────────`)
      process.exit(0)
    }, 100)
  })

  process.on('SIGTERM', () => {
    running = false
  })

  const interval = cfg.interval
  console.log(`\n  Infinite XT Stress Test`)
  console.log(`  ──────────────────────`)
  console.log(`  asset:     ${cfg.asset}`)
  console.log(`  direction: ${cfg.direction}`)
  console.log(`  interval:  ${interval}ms (${(1000 / interval).toFixed(0)} tx/s target)`)
  console.log(`  amount:    ${cfg.amount}`)
  console.log(`  source nonce: ${nonceSource}`)
  console.log(`  dest nonce:   ${nonceDest}`)
  console.log(`  Press Ctrl+C to stop\n`)

  let nextTime = Date.now()
  let lastReport = Date.now()
  let reportCount = 0

  while (running) {
    const sessionId = generateSessionId()
    const txs = {}
    let txSourceBytes, txDestBytes

    try {
      if (isERC20) {
        if (isAToB) {
          const approve = await buildApprove(tokenSource, bridgeSource, parsedAmount, signerSource, sourceChainId, nonceSource)
          const bridge = await buildBridgeERC20(bridgeSource, destChainId, tokenSource, parsedAmount, receiverDest, sessionId, signerSource, sourceChainId, nonceSource + 1)
          const recv = await buildReceiveTokens(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nonceDest)
          txs[sourceChainId] = [approve, bridge]
          txs[destChainId] = [recv]
          txSourceBytes = bridge
          txDestBytes = recv
        } else {
          const approve = await buildApprove(tokenSource, bridgeSource, parsedAmount, signerSource, sourceChainId, nonceDest)
          const bridge = await buildBridgeERC20(bridgeSource, destChainId, tokenSource, parsedAmount, receiverDest, sessionId, signerSource, sourceChainId, nonceDest + 1)
          const recv = await buildReceiveTokens(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nonceSource)
          txs[sourceChainId] = [approve, bridge]
          txs[destChainId] = [recv]
          txSourceBytes = bridge
          txDestBytes = recv
        }
      } else {
        if (isAToB) {
          const bridge = await buildBridgeEth(bridgeSource, destChainId, receiverDest, sessionId, parsedAmount, signerSource, sourceChainId, nonceSource)
          const recv = await buildReceiveEth(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nonceDest)
          txs[sourceChainId] = [bridge]
          txs[destChainId] = [recv]
          txSourceBytes = bridge
          txDestBytes = recv
        } else {
          const bridge = await buildBridgeEth(bridgeSource, destChainId, receiverDest, sessionId, parsedAmount, signerSource, sourceChainId, nonceDest)
          const recv = await buildReceiveEth(bridgeDest, sessionId, sourceChainId, destChainId, bridgeSource, receiverDest, signerDest, destChainId, nonceSource)
          txs[sourceChainId] = [bridge]
          txs[destChainId] = [recv]
          txSourceBytes = bridge
          txDestBytes = recv
        }
      }

      await submitXT(cfg.SIDECAR_A_URL, txs)
      submitted++

      if (isAToB) {
        nonceSource += sourceStride
        nonceDest += 1
      } else {
        nonceSource += 1
        nonceDest += sourceStride
      }
    } catch (err) {
      failed++
      const msg = err.message

      if (msg.includes('conflict') || msg.includes('nonce')) {
        conflicts++
        try {
          nonceSource = await providerSource.getTransactionCount(await signerSource.getAddress(), 'pending')
          nonceDest = await providerDest.getTransactionCount(await signerDest.getAddress(), 'pending')
        } catch {}
      } else if (msg.includes('fetch failed') || msg.includes('ECONNREFUSED')) {
        if (failed % 10 === 1) process.stderr.write(`\r  ⚠ connection error — retrying...`)
      }

      if (running && failed < 3) await new Promise(r => setTimeout(r, 50))
    }

    reportCount++
    if (reportCount >= 100) {
      const elapsed = (Date.now() - lastReport) / 1000
      const rate = elapsed > 0 ? (100 / elapsed).toFixed(1) : '?'
      process.stdout.write(`\r  submitted: ${submitted} | failed: ${failed} | conflicts: ${conflicts} | ${rate} tx/s  `)
      lastReport = Date.now()
      reportCount = 0
    }

    if (running) {
      nextTime += interval
      const delay = nextTime - Date.now()
      if (delay > 0) await new Promise(r => setTimeout(r, delay))
    }
  }
}

main().catch(e => { console.error(e); process.exit(1) })

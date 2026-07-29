#!/usr/bin/env node

import { ethers } from 'ethers'

const DEFAULTS = {
  CHAIN_A_ID: 77777,
  CHAIN_B_ID: 88888,
  CHAIN_A_RPC: 'http://127.0.0.1:17545',
  CHAIN_B_RPC: 'http://127.0.0.1:27545',
  PRIVATE_KEY: '0xb5685871a40a5c19507b67f858941d9d6bed5012c37eee572aff7356eb316174',
  CHAIN_A_BRIDGE: '0xa21afbba5c0dc05fbfbc534906862074344d2aa1',
  CHAIN_B_BRIDGE: '0xa21afbba5c0dc05fbfbc534906862074344d2aa1',
  CHAIN_A_TOKEN: '0x73af42789cdfa96d8cd46cdb2458821292f10b9f',
  CHAIN_B_TOKEN: '0x73af42789cdfa96d8cd46cdb2458821292f10b9f',
  CHAIN_A_ETH_LIQUIDITY: '0x0382bc1c7e9df089adf6b1335d015e28d1f54b69',
  CHAIN_B_ETH_LIQUIDITY: '0x0382bc1c7e9df089adf6b1335d015e28d1f54b69',
}

const ERC20_ABI = [
  'function balanceOf(address owner) view returns (uint256)',
  'function mint(address to, uint256 amount)',
]
const ETH_LIQUIDITY_ABI = ['function fund() payable']

const DEFAULT_MAX_FEE = ethers.parseUnits('20', 'gwei')
const DEFAULT_PRIORITY_FEE = ethers.parseUnits('1', 'gwei')

async function signTx(signer, txReq, chainId, nonce) {
  const fee = await signer.provider.getFeeData()
  return signer.signTransaction({
    ...txReq,
    chainId: BigInt(chainId),
    nonce,
    type: 2,
    maxFeePerGas: fee.maxFeePerGas ?? DEFAULT_MAX_FEE,
    maxPriorityFeePerGas: fee.maxPriorityFeePerGas ?? DEFAULT_PRIORITY_FEE,
    from: await signer.getAddress(),
  })
}

async function buildMintTx(tokenAddr, toAddr, amount, signer, chainId, nonce) {
  const c = new ethers.Contract(tokenAddr, ERC20_ABI, signer)
  const tx = await c.mint.populateTransaction(toAddr, amount)
  return signTx(signer, { ...tx, gasLimit: 900000n }, chainId, nonce)
}

async function buildFundLiquidityTx(liqAddr, value, signer, chainId, nonce) {
  const c = new ethers.Contract(liqAddr, ETH_LIQUIDITY_ABI, signer)
  const tx = await c.fund.populateTransaction({ value })
  return signTx(signer, { ...tx, gasLimit: 120000n }, chainId, nonce)
}

async function broadcastAndWait(provider, signedTx, label) {
  const resp = await provider.broadcastTransaction(signedTx)
  const receipt = await provider.waitForTransaction(resp.hash, 1, 30000)
  if (!receipt) throw new Error(`${label}: tx ${resp.hash} not found`)
  if (receipt.status !== 1) throw new Error(`${label}: tx ${resp.hash} failed (status ${receipt.status})`)
  process.stdout.write(`${label}: ${resp.hash} ✓\n`)
  return receipt
}

async function main() {
  const a = process.argv.slice(2)
  let amount = '1'

  for (let i = 0; i < a.length; i++) {
    if (a[i] === '--amount') amount = a[++i]
    else if (a[i] === '--pk') DEFAULTS.PRIVATE_KEY = a[++i]
    else if (a[i] === '--help' || a[i] === '-h') {
      console.log('Usage: node scripts/seed-liquidity.mjs --amount <tokens|ETH>')
      console.log('  Mints ERC20 tokens and seeds ETH liquidity pools on both chains.')
      process.exit(0)
    }
  }

  const value = ethers.parseEther(amount)
  if (value <= 0n) { console.error('Amount must be > 0'); process.exit(1) }

  const providerA = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_A_RPC)
  const providerB = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_B_RPC)
  const signerA = new ethers.Wallet(DEFAULTS.PRIVATE_KEY, providerA)
  const signerB = new ethers.Wallet(DEFAULTS.PRIVATE_KEY, providerB)
  const addr = await signerA.getAddress()

  const nonceA = await providerA.getTransactionCount(addr, 'pending')
  const nonceB = await providerB.getTransactionCount(addr, 'pending')

  // Mint ERC20 tokens on both chains so the wallet has tokens to bridge
  console.log(`\n  Minting ${amount} ERC20 tokens on each chain...`)
  const mintA = await buildMintTx(DEFAULTS.CHAIN_A_TOKEN, addr, value, signerA, DEFAULTS.CHAIN_A_ID, nonceA)
  const mintB = await buildMintTx(DEFAULTS.CHAIN_B_TOKEN, addr, value, signerB, DEFAULTS.CHAIN_B_ID, nonceB)
  await Promise.all([
    broadcastAndWait(providerA, mintA, 'Chain A mint'),
    broadcastAndWait(providerB, mintB, 'Chain B mint'),
  ])

  // Check ERC20 balances after mint
  const tokenA = new ethers.Contract(DEFAULTS.CHAIN_A_TOKEN, ERC20_ABI, providerA)
  const tokenB = new ethers.Contract(DEFAULTS.CHAIN_B_TOKEN, ERC20_ABI, providerB)
  const [balA, balB] = await Promise.all([
    tokenA.balanceOf(addr),
    tokenB.balanceOf(addr),
  ])
  console.log(`  Wallet: ${ethers.formatEther(balA)} tokens on A | ${ethers.formatEther(balB)} tokens on B`)

  // Fund ETH liquidity pools so ETH bridging works
  console.log(`\n  Seeding ${amount} ETH to each liquidity pool...`)
  const liqNonceA = await providerA.getTransactionCount(addr, 'pending')
  const liqNonceB = await providerB.getTransactionCount(addr, 'pending')
  const fundA = await buildFundLiquidityTx(DEFAULTS.CHAIN_A_ETH_LIQUIDITY, value, signerA, DEFAULTS.CHAIN_A_ID, liqNonceA)
  const fundB = await buildFundLiquidityTx(DEFAULTS.CHAIN_B_ETH_LIQUIDITY, value, signerB, DEFAULTS.CHAIN_B_ID, liqNonceB)
  await Promise.all([
    broadcastAndWait(providerA, fundA, 'Chain A pool'),
    broadcastAndWait(providerB, fundB, 'Chain B pool'),
  ])

  const [poolA, poolB] = await Promise.all([
    providerA.getBalance(DEFAULTS.CHAIN_A_ETH_LIQUIDITY),
    providerB.getBalance(DEFAULTS.CHAIN_B_ETH_LIQUIDITY),
  ])
  console.log(`  Pool A: ${ethers.formatEther(poolA)} ETH  |  Pool B: ${ethers.formatEther(poolB)} ETH\n`)
}

main().catch(e => { console.error(e); process.exit(1) })

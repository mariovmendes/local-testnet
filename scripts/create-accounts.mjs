import { ethers } from 'ethers'
import fs from 'node:fs'

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
  AMOUNT_ACCOUNTS: 10000
}

class Account {
    constructor(address, privateKey){
        this.address = address;
        this.privateKey = privateKey;
    }
}

const ERC20_ABI = [
  'function balanceOf(address owner) view returns (uint256)',
  'function approve(address spender, uint256 amount) returns (bool)',
  'function mint(address to, uint256 amount)',
]

const BRIDGE_ABI = [
  'function bridgeERC20To(uint256 chainDest, address tokenSrc, uint256 amount, address receiver, uint256 sessionId)',
  'function bridgeEthTo(uint256 sessionId, uint256 chainDest, address receiver) payable',
  'function receiveTokens(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (address token, uint256 amount)',
  'function receiveETH(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (uint256 amount)',
]

const ETH_LIQUIDITY_ABI = ['function fund() payable']

const DEFAULT_MAX_FEE = ethers.parseUnits('20', 'gwei')
const DEFAULT_PRIORITY_FEE = ethers.parseUnits('1', 'gwei')

// Covers worst-case gas cost of the approve+bridgeERC20To (chain A) and
// receiveTokens (chain B) calls in generate-transactions.mjs, at DEFAULT_MAX_FEE.
const ETH_FUND_CHAIN_A = ethers.parseEther('0.05')
const ETH_FUND_CHAIN_B = ethers.parseEther('0.1')

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

async function buildFundEthTx(toAddr, amount, signer, chainId, nonce) {
  return signTx(signer, { to: toAddr, value: amount, gasLimit: 21000n }, chainId, nonce)
}

async function signAndBroadcastAll(provider, signer, chainId, startNonce, accounts, buildTx, label) {
  const signedTxs = []
  for (let i = 0; i < accounts.length; i++) {
    signedTxs.push(await buildTx(accounts[i], signer, chainId, startNonce + i))
  }

  console.log(`  Broadcasting ${signedTxs.length} ${label} txs`)
  await Promise.all(
    signedTxs.map((tx, i) => provider.broadcastTransaction(tx).catch(e => {
      console.error(`  ${label} tx ${i} (nonce ${startNonce + i}) failed to broadcast:`, e.message)
    }))
  )

  console.log(`  Waiting for final ${label} tx (nonce ${startNonce + signedTxs.length - 1}) to confirm...`)
  const receipt = await provider.waitForTransaction(
    ethers.Transaction.from(signedTxs.at(-1)).hash, 1, 60000
  )
  console.log(receipt?.status === 1 ? `  All ${label} txs confirmed ✓` : `  Last ${label} tx failed ✗`)
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
      let accounts = [];
      const value = ethers.parseEther('1000');
      console.log("GENERATING ACCOUNTS "+DEFAULTS.AMOUNT_ACCOUNTS)
      for(let i=0;i<DEFAULTS.AMOUNT_ACCOUNTS;i++){
        const wallet = ethers.Wallet.createRandom()
        accounts.push(new Account(wallet.address, wallet.privateKey));
      }
      fs.writeFile('accounts.json', JSON.stringify(accounts), (err) => {
              if (err) {
                  console.error('Error adding account to file:', err);
              }
      });
      const providerA = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_A_RPC) // rollup-a
      const providerB = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_B_RPC) // rollup-b
      const signerA = new ethers.Wallet(DEFAULTS.PRIVATE_KEY, providerA)
      const signerB = new ethers.Wallet(DEFAULTS.PRIVATE_KEY, providerB)
      const nonceA = await providerA.getTransactionCount(await signerA.getAddress(), 'pending')
      const nonceB = await providerB.getTransactionCount(await signerB.getAddress(), 'pending')

      console.log(`\n  Minting 1000 ERC20 tokens on chain A for all accounts`)
      await signAndBroadcastAll(
        providerA, signerA, DEFAULTS.CHAIN_A_ID, nonceA, accounts,
        (acct, signer, chainId, nonce) => buildMintTx(DEFAULTS.CHAIN_A_TOKEN, acct.address, value, signer, chainId, nonce),
        'mint'
      )

      console.log(`\n  Funding ETH on chain A for all accounts`)
      await signAndBroadcastAll(
        providerA, signerA, DEFAULTS.CHAIN_A_ID, nonceA + DEFAULTS.AMOUNT_ACCOUNTS, accounts,
        (acct, signer, chainId, nonce) => buildFundEthTx(acct.address, ETH_FUND_CHAIN_A, signer, chainId, nonce),
        'fund-eth-a'
      )

      console.log(`\n  Funding ETH on chain B for all accounts`)
      await signAndBroadcastAll(
        providerB, signerB, DEFAULTS.CHAIN_B_ID, nonceB, accounts,
        (acct, signer, chainId, nonce) => buildFundEthTx(acct.address, ETH_FUND_CHAIN_B, signer, chainId, nonce),
        'fund-eth-b'
      )
}

main().catch(e => { console.error(e); process.exit(1) })
import { ethers } from 'ethers'
import fs from 'node:fs'

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
  INTERVAL: 500
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


function generateSessionId() {
  const b = new Uint8Array(32)
  crypto.getRandomValues(b)
  return '0x' + Array.from(b, x => x.toString(16).padStart(2, '0')).join('')
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

async function buildReceiveTokens(bridgeAddr, sessionId, chainSrc, chainDest, bridgeSrc, receiver, signer, chainId, nonce) {
  const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
  const tx = await c.receiveTokens.populateTransaction({
    chainSrc, chainDest, sender: bridgeSrc, receiver, sessionId, label: 'SEND_TOKENS',
  })
  return signTx(signer, { ...tx, gasLimit: DEFAULT_RECEIVE_GAS }, chainId, nonce)
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


async function main(){
    let accounts = JSON.parse(fs.readFileSync('accounts.json', 'utf8'));
    accounts.forEach(element => {
        element.nonce ??= 0;
        element.recvNonce ??= 0;
        element.balance = 1000;
    });
    console.log(accounts[0]);

    function saveAccounts() {
        fs.writeFileSync('accounts.json', JSON.stringify(accounts))
        console.log(`  Saved nonces for ${accounts.length} accounts to accounts.json`)
    }

    let nextTime = Date.now()
    const startTime = Date.now()
    let submitted = 0
    let failed = 0
    let running = true

    process.on('SIGINT', () => {
        running = false
        setTimeout(() => {
            const elapsed = ((Date.now() - startTime) / 1000).toFixed(1)
            const rate = elapsed > 0 ? (submitted / parseFloat(elapsed)).toFixed(1) : '0'
            const full_rate = elapsed > 0 ? ((submitted + failed) / parseFloat(elapsed)).toFixed(1) : '0'
            console.log(`\n\n  ────────────────────────────────────`)
            console.log(`  submitted: ${submitted}`)
            console.log(`  failed:    ${failed}`)
            console.log(`  ${elapsed}s · ${rate} tx/s`)
            console.log(`  Full rate: ${full_rate} tx/s`)
            console.log(`  ────────────────────────────────────`)
            saveAccounts()
            process.exit(0)
        }, 100)
    })

    process.on('SIGTERM', () => {
        running = false
        saveAccounts()
    })

    const providerSource = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_A_RPC)
    const providerDest = new ethers.JsonRpcProvider(DEFAULTS.CHAIN_B_RPC)

    while(running){
      console.log("Starting a new iteration:")
      for(const element of accounts) {
        try{
            console.log("Generating a new cross-chain transaction for account: " + element.address)
            const txs = {}
            const signerSource = new ethers.Wallet(element.privateKey, providerSource)
            const signerDest = new ethers.Wallet(element.privateKey, providerDest)
            const parsedAmount = ethers.parseUnits('1', 18)
            const sessionId = generateSessionId()
            const approve = await buildApprove(DEFAULTS.CHAIN_A_TOKEN, DEFAULTS.CHAIN_A_BRIDGE, parsedAmount , signerSource, DEFAULTS.CHAIN_A_ID, element.nonce)
            const bridge = await buildBridgeERC20(DEFAULTS.CHAIN_A_BRIDGE, DEFAULTS.CHAIN_B_ID, DEFAULTS.CHAIN_A_TOKEN , parsedAmount, element.address, sessionId, signerSource, DEFAULTS.CHAIN_A_ID, element.nonce + 1)
            element.nonce += 2
            const recv = await buildReceiveTokens(DEFAULTS.CHAIN_B_BRIDGE, sessionId, DEFAULTS.CHAIN_A_ID, DEFAULTS.CHAIN_B_ID, DEFAULTS.CHAIN_A_BRIDGE, element.address, signerDest, DEFAULTS.CHAIN_B_ID, element.recvNonce)
            element.recvNonce++
            txs[DEFAULTS.CHAIN_A_ID] = [approve, bridge]
            txs[DEFAULTS.CHAIN_B_ID] = [recv]
            // Fire-and-forget: don't block the pacing loop on the publisher's response.
            submitXT(DEFAULTS.SIDECAR_A_URL, txs)
              .then(() => { submitted++ })
              .catch(err => { failed++; console.log(`submitXT failed for ${element.address}: ${err.message}`) })
            nextTime += DEFAULTS.INTERVAL
            const delay = nextTime - Date.now()
            if (delay > 0) await new Promise(r => setTimeout(r, delay))
        } catch(error){
            const msg = error.message
            console.log(msg)
        }
      }
    }
}

main().catch(e => { console.error(e); process.exit(1) })
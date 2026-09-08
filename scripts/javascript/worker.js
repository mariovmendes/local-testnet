import crypto from 'crypto';
import fs from 'node:fs';
import { ethers } from 'ethers';
import { parentPort, workerData } from 'worker_threads';

const { chunk, workerId } = workerData;

// One JSONL record per submitted XT, so `trace-xt.py` can join what this
// script sent (session id, addresses, nonces, tx hashes) with the
// instance_id the sidecar assigned and every XTFLOW line downstream.
const SUBMISSIONS_FILE = `xt-submissions-w${workerId}.jsonl`

function recordSubmission(record) {
    try {
        fs.appendFileSync(SUBMISSIONS_FILE, JSON.stringify(record) + '\n')
    } catch (err) {
        log(`failed to record submission: ${err.message}`)
    }
}

function txHash(signedRawTx) {
    return ethers.Transaction.from(signedRawTx).hash
}
let submitted = 0
let failed = 0
let processed = 0
let running = true

const RPC_TIMEOUT_MS = 10_000
const SUBMIT_TIMEOUT_MS = 10_000
const HEARTBEAT_MS = 5_000

function log(msg) {
    console.log(`[worker ${workerId}] ${msg}`)
}

function makeProvider(url) {
    const req = new ethers.FetchRequest(url)
    req.timeout = RPC_TIMEOUT_MS
    return new ethers.JsonRpcProvider(req)
}

const DEFAULTS = {
    CHAIN_A_ID: 77777,
    CHAIN_B_ID: 88888,
    CHAIN_A_RPC: 'http://127.0.0.1:17545',
    CHAIN_B_RPC: 'http://127.0.0.1:27545',
    SIDECAR_A_URL: 'http://127.0.0.1:17090',
    PRIVATE_KEY: '0xb5685871a40a5c19507b67f858941d9d6bed5012c37eee572aff7356eb316174',
    CHAIN_A_BRIDGE: '0x24BF35E5687A67Bf0943599Ff1BF4Ed0E5D22446',
    CHAIN_B_BRIDGE: '0x24BF35E5687A67Bf0943599Ff1BF4Ed0E5D22446',
    CHAIN_A_TOKEN: '0x71C63b81Cb9fBE86D141f413e81b2C97a97eEe33',
    CHAIN_B_TOKEN: '0x71C63b81Cb9fBE86D141f413e81b2C97a97eEe33',
    CHAIN_A_ETH_LIQUIDITY: '0xa21aFBbA5C0dc05fbFBC534906862074344D2AA1',
    CHAIN_B_ETH_LIQUIDITY: '0xa21aFBbA5C0dc05fbFBC534906862074344D2AA1',
    INTERVAL: 400
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
// Right-sized gas limits: the builder escrows gas_limit x max_fee per tx
// when validating affordability, so oversized limits (900k approve / 3M
// receive) made each round's escrow a large fraction of account funding.
const DEFAULT_APPROVE_GAS = 100_000n
const DEFAULT_BRIDGE_GAS = 900_000n
const DEFAULT_RECEIVE_GAS = 3_000_000n


function generateSessionId() {
    return '0x' + crypto.randomBytes(32).toString('hex')
}

async function buildApprove(tokenAddr, spender, amount, signer, chainId, nonce) {
    const c = new ethers.Contract(tokenAddr, ERC20_ABI, signer)
    const tx = await c.approve.populateTransaction(spender, amount)
    return signTx(signer, { ...tx, gasLimit: DEFAULT_APPROVE_GAS }, chainId, nonce)
}

async function buildBridgeERC20(bridgeAddr, chainDest, tokenAddr, amount, receiver, sessionId, signer, chainId, nonce) {
    const c = new ethers.Contract(bridgeAddr, BRIDGE_ABI, signer)
    const tx = await c.bridgeERC20To.populateTransaction(chainDest, tokenAddr, amount, receiver, sessionId)
    return signTx(signer, { ...tx, gasLimit: DEFAULT_BRIDGE_GAS }, chainId, nonce)
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
    const controller = new AbortController()
    const timeout = setTimeout(() => controller.abort(), SUBMIT_TIMEOUT_MS)
    try {
        const res = await fetch(`${sidecarUrl}/xt`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ transactions: txs }),
            signal: controller.signal,
        })
        if (!res.ok) {
            const body = await res.text()
            throw new Error(`submitXT ${res.status}: ${body}`)
        }
        return res.json()
    } catch (err) {
        if (err.name === 'AbortError') throw new Error(`submitXT timed out after ${SUBMIT_TIMEOUT_MS}ms`)
        throw err
    } finally {
        clearTimeout(timeout)
    }
}

parentPort.on('message', (msg) => {
    if (msg.type === 'stop') {
        log('stop requested, finishing current account then exiting...')
        running = false;
    }
});

const heartbeat = setInterval(() => {
    parentPort.postMessage({ type: 'progress', data: { processed, submitted, failed } })
}, HEARTBEAT_MS)

async function main() {
    log(`starting with ${chunk.length} accounts (RPC timeout ${RPC_TIMEOUT_MS}ms, submit timeout ${SUBMIT_TIMEOUT_MS}ms)`)
    fs.writeFileSync(SUBMISSIONS_FILE, '')
    log(`recording submissions to ${SUBMISSIONS_FILE}`)
    let nextTime = Date.now();
    const providerSource = makeProvider(DEFAULTS.CHAIN_A_RPC)
    const providerDest = makeProvider(DEFAULTS.CHAIN_B_RPC)

    while (running) {
        for (const element of chunk) {
            if (!running) break;
            try {
                const txs = {}
                const signerSource = new ethers.Wallet(element.privateKey, providerSource)
                const signerDest = new ethers.Wallet(element.privateKey, providerDest)
                const parsedAmount = ethers.parseUnits('1', 18)
                const sessionId = generateSessionId()
                // Always fetch the real "pending" nonce from each chain right
                // before building this round's transactions, rather than
                // trusting a locally-incremented counter. op-rbuilder's
                // "pending" view already merges on-chain state with anything
                // still queued/reserved (including cross-chain XT
                // reservations), so this self-heals after any failed or
                // aborted round instead of drifting out of sync with reality
                // forever (a prior round whose submission failed, or whose
                // instance was later voted down, would otherwise permanently
                // desync a local counter from what actually landed on-chain).
                const sourceNonce = await signerSource.getNonce('pending')
                const destNonce = await signerDest.getNonce('pending')
                const approve = await buildApprove(DEFAULTS.CHAIN_A_TOKEN, DEFAULTS.CHAIN_A_BRIDGE, parsedAmount, signerSource, DEFAULTS.CHAIN_A_ID, sourceNonce)
                const bridge = await buildBridgeERC20(DEFAULTS.CHAIN_A_BRIDGE, DEFAULTS.CHAIN_B_ID, DEFAULTS.CHAIN_A_TOKEN, parsedAmount, element.address, sessionId, signerSource, DEFAULTS.CHAIN_A_ID, sourceNonce + 1)
                const recv = await buildReceiveTokens(DEFAULTS.CHAIN_B_BRIDGE, sessionId, DEFAULTS.CHAIN_A_ID, DEFAULTS.CHAIN_B_ID, DEFAULTS.CHAIN_A_BRIDGE, element.address, signerDest, DEFAULTS.CHAIN_B_ID, destNonce)
                txs[DEFAULTS.CHAIN_A_ID] = [approve, bridge]
                txs[DEFAULTS.CHAIN_B_ID] = [recv]
                processed++
                const seq = processed
                const sentAt = Date.now()
                const record = {
                    worker: workerId,
                    seq,
                    sent_at: new Date(sentAt).toISOString(),
                    address: element.address,
                    session_id: sessionId,
                    source_chain: DEFAULTS.CHAIN_A_ID,
                    dest_chain: DEFAULTS.CHAIN_B_ID,
                    source_nonce: sourceNonce,
                    dest_nonce: destNonce,
                    approve_tx: txHash(approve),
                    bridge_tx: txHash(bridge),
                    receive_tx: txHash(recv),
                }
                // Fire-and-forget: don't block the pacing loop on the publisher's response.
                submitXT(DEFAULTS.SIDECAR_A_URL, txs)
                    .then(res => {
                        submitted++
                        recordSubmission({
                            ...record,
                            instance_id: res.instance_id,
                            status: res.status,
                            latency_ms: Date.now() - sentAt,
                        })
                    })
                    .catch(err => {
                        failed++
                        recordSubmission({
                            ...record,
                            instance_id: null,
                            status: 'failed',
                            error: err.message,
                            latency_ms: Date.now() - sentAt,
                        })
                        log(`submitXT failed for ${element.address}: ${err.message}`)
                    })
                nextTime += DEFAULTS.INTERVAL
                const delay = nextTime - Date.now()
                if (delay > 0) await new Promise(r => setTimeout(r, delay))
            } catch (error) {
                processed++
                log(`build/sign error for ${element.address}: ${error.message}`)
                parentPort.postMessage({ type: 'error', data: error.message });
            }
        }
    }

    clearInterval(heartbeat)
    log(`done: processed=${processed} submitted=${submitted} failed=${failed}`)
    parentPort.postMessage({ type: 'done', data: {accounts: chunk, submitted, failed} });
}

main().catch(e => {
    clearInterval(heartbeat)
    log(`crashed: ${e.message}`)
    console.error('Worker failed:', e);
    parentPort.postMessage({ type: 'partial', data: {accounts: chunk, submitted, failed} });
})

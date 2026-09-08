import { ethers } from 'ethers'
import {
  CHAIN_A_BRIDGE_ADDRESS,
  CHAIN_A_ETH_LIQUIDITY_ADDRESS,
  CHAIN_A_ID,
  CHAIN_A_PRIVATE_KEY,
  CHAIN_A_RPC,
  CHAIN_A_TOKEN_ADDRESS,
  CHAIN_B_BRIDGE_ADDRESS,
  CHAIN_B_ETH_LIQUIDITY_ADDRESS,
  CHAIN_B_ID,
  CHAIN_B_PRIVATE_KEY,
  CHAIN_B_RPC,
  CHAIN_B_TOKEN_ADDRESS,
  CHAIN_A_BLOCKSCOUT,
  CHAIN_B_BLOCKSCOUT,
  CET_FACTORY_ADDRESS,
} from '../config/chains'

export { CHAIN_A_ID, CHAIN_B_ID, CHAIN_A_BLOCKSCOUT, CHAIN_B_BLOCKSCOUT }

const ERC20_ABI = [
  'function balanceOf(address owner) view returns (uint256)',
  'function mint(address to, uint256 amount)',
  'function transfer(address to, uint256 amount) returns (bool)',
  'function approve(address spender, uint256 amount) returns (bool)',
]

const BRIDGE_ABI = [
  'function bridgeERC20To(uint256 chainDest, address tokenSrc, uint256 amount, address receiver, uint256 sessionId)',
  'function bridgeEthTo(uint256 sessionId, uint256 chainDest, address receiver) payable',
  'function receiveTokens(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (address token, uint256 amount)',
  'function receiveETH(tuple(uint256 chainSrc, uint256 chainDest, address sender, address receiver, uint256 sessionId, string label) msgHeader) returns (uint256 amount)',
]

const CET_FACTORY_ABI = [
  'function predictAddress(address remoteAsset, uint256 remoteChainID) view returns (address)',
]

const ETH_LIQUIDITY_ABI = [
  'function fund() payable',
]

const providerCache = new Map<string, ethers.JsonRpcProvider>()

function resolveRpcUrl(url: string): string {
  if (/^https?:\/\//i.test(url)) {
    return url
  }
  if (typeof window !== 'undefined') {
    return new URL(url, window.location.origin).toString()
  }
  return url
}

export function getProvider(chain: 'A' | 'B'): ethers.JsonRpcProvider {
  const url = resolveRpcUrl(chain === 'A' ? CHAIN_A_RPC : CHAIN_B_RPC)
  let provider = providerCache.get(url)
  if (!provider) {
    provider = new ethers.JsonRpcProvider(url)
    providerCache.set(url, provider)
  }
  return provider
}

export function getChainId(chain: 'A' | 'B'): number {
  return chain === 'A' ? CHAIN_A_ID : CHAIN_B_ID
}

export function getTokenAddress(chain: 'A' | 'B'): string {
  const address = chain === 'A' ? CHAIN_A_TOKEN_ADDRESS : CHAIN_B_TOKEN_ADDRESS
  if (!address) {
    throw new Error(
      `Missing token address for chain ${chain}. Set VITE_CHAIN_${chain}_TOKEN_ADDRESS.`
    )
  }
  return address
}

export function getBridgeAddress(chain: 'A' | 'B'): string {
  const address = chain === 'A' ? CHAIN_A_BRIDGE_ADDRESS : CHAIN_B_BRIDGE_ADDRESS
  if (!address) {
    throw new Error(
      `Missing bridge address for chain ${chain}. Set VITE_CHAIN_${chain}_BRIDGE_ADDRESS.`
    )
  }
  return address
}

export function getCetFactoryAddress(): string {
  if (!CET_FACTORY_ADDRESS) {
    throw new Error('Missing CET factory address. Set VITE_CET_FACTORY_ADDRESS.')
  }
  return CET_FACTORY_ADDRESS
}

export function getEthLiquidityAddress(chain: 'A' | 'B'): string {
  const address =
    chain === 'A' ? CHAIN_A_ETH_LIQUIDITY_ADDRESS : CHAIN_B_ETH_LIQUIDITY_ADDRESS
  if (!address) {
    throw new Error(
      `Missing ETH liquidity address for chain ${chain}. Set VITE_CHAIN_${chain}_ETH_LIQUIDITY_ADDRESS.`
    )
  }
  return address
}

export async function getEthLiquidityBalance(chain: 'A' | 'B'): Promise<bigint> {
  return getProvider(chain).getBalance(getEthLiquidityAddress(chain))
}

export function getSigner(chain: 'A' | 'B'): ethers.Wallet {
  const privateKey = chain === 'A' ? CHAIN_A_PRIVATE_KEY : CHAIN_B_PRIVATE_KEY
  if (!privateKey) {
    throw new Error(
      `Missing private key for chain ${chain}. Set VITE_WALLET_PRIVATE_KEY or VITE_CHAIN_${chain}_PRIVATE_KEY.`
    )
  }
  return new ethers.Wallet(privateKey, getProvider(chain))
}

const DEFAULT_MAX_FEE_PER_GAS = ethers.parseUnits('20', 'gwei')
const DEFAULT_MAX_PRIORITY_FEE_PER_GAS = ethers.parseUnits('1', 'gwei')
const DEFAULT_BRIDGE_RECEIVE_GAS_LIMIT = 3_000_000n

export async function getTokenBalance(
  tokenAddress: string,
  walletAddress: string,
  chain: 'A' | 'B'
): Promise<bigint> {
  const provider = getProvider(chain)
  const token = new ethers.Contract(tokenAddress, ERC20_ABI, provider)
  return token.balanceOf(walletAddress)
}

export async function getWrappedTokenAddress(
  remoteTokenAddress: string,
  remoteChainId: number,
  localChain: 'A' | 'B'
): Promise<string> {
  const factory = new ethers.Contract(
    getCetFactoryAddress(),
    CET_FACTORY_ABI,
    getProvider(localChain)
  )
  return factory.predictAddress(remoteTokenAddress, remoteChainId)
}

export async function buildMintTx(
  tokenAddress: string,
  toAddress: string,
  amount: bigint,
  signer: ethers.Wallet,
  chainId: number
): Promise<string> {
  const token = new ethers.Contract(tokenAddress, ERC20_ABI, signer)
  const tx = await token.mint.populateTransaction(toAddress, amount)

  return signContractTx(signer, {
    ...tx,
    gasLimit: 900000n,
  }, BigInt(chainId))
}

export async function buildApproveTx(
  tokenAddress: string,
  spenderAddress: string,
  amount: bigint,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number
): Promise<string> {
  const token = new ethers.Contract(tokenAddress, ERC20_ABI, signer)
  const tx = await token.approve.populateTransaction(spenderAddress, amount)

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: 900000n,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildBridgeERC20ToTx(
  bridgeAddress: string,
  chainDest: number,
  tokenAddress: string,
  amount: bigint,
  receiver: string,
  sessionId: string,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number
): Promise<string> {
  const bridge = new ethers.Contract(bridgeAddress, BRIDGE_ABI, signer)
  const tx = await bridge.bridgeERC20To.populateTransaction(
    chainDest,
    tokenAddress,
    amount,
    receiver,
    sessionId
  )

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: 900000n,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildBridgeEthToTx(
  bridgeAddress: string,
  chainDest: number,
  receiver: string,
  sessionId: string,
  value: bigint,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number
): Promise<string> {
  const bridge = new ethers.Contract(bridgeAddress, BRIDGE_ABI, signer)
  const tx = await bridge.bridgeEthTo.populateTransaction(
    sessionId,
    chainDest,
    receiver,
    { value }
  )

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: 900000n,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildBridgeReceiveEthTx(
  bridgeAddress: string,
  sessionId: string,
  chainSrc: number,
  chainDest: number,
  bridgeSrc: string,
  receiver: string,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number,
  gasOverride?: bigint
): Promise<string> {
  const bridge = new ethers.Contract(bridgeAddress, BRIDGE_ABI, signer)
  const msgHeader = {
    chainSrc,
    chainDest,
    sender: bridgeSrc,
    receiver,
    sessionId,
    label: 'SEND_ETH',
  }
  const tx = await bridge.receiveETH.populateTransaction(msgHeader)

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: gasOverride ?? DEFAULT_BRIDGE_RECEIVE_GAS_LIMIT,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildFundEthLiquidityTx(
  liquidityAddress: string,
  value: bigint,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number
): Promise<string> {
  const liquidity = new ethers.Contract(liquidityAddress, ETH_LIQUIDITY_ABI, signer)
  const tx = await liquidity.fund.populateTransaction({ value })

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: 120000n,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildBridgeReceiveTokensTx(
  bridgeAddress: string,
  sessionId: string,
  chainSrc: number,
  chainDest: number,
  bridgeSrc: string,
  receiver: string,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number,
  gasOverride?: bigint
): Promise<string> {
  const bridge = new ethers.Contract(bridgeAddress, BRIDGE_ABI, signer)
  const msgHeader = {
    sessionId,
    chainSrc,
    chainDest,
    sender: bridgeSrc,
    receiver,
    label: 'SEND_TOKENS',
  }
  const tx = await bridge.receiveTokens.populateTransaction(msgHeader)

  return signContractTx(
    signer,
    {
      ...tx,
      gasLimit: gasOverride ?? DEFAULT_BRIDGE_RECEIVE_GAS_LIMIT,
    },
    BigInt(chainId),
    nonceOverride
  )
}

export async function buildNativeTransferTx(
  toAddress: string,
  value: bigint,
  signer: ethers.Wallet,
  chainId: number,
  nonceOverride?: number
): Promise<string> {
  return signContractTx(
    signer,
    {
      to: toAddress,
      value,
      gasLimit: 21000n,
      data: '0x',
    },
    BigInt(chainId),
    nonceOverride
  )
}

export function formatBalance(balance: bigint, decimals: number = 18): string {
  return ethers.formatUnits(balance, decimals)
}

export function parseAmount(amount: string, decimals: number = 18): bigint {
  return ethers.parseUnits(amount, decimals)
}

export function generateSessionId(): string {
  const randomBytes = new Uint8Array(32)
  crypto.getRandomValues(randomBytes)
  return '0x' + Array.from(randomBytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

export async function waitForTransactionReceipt(
  provider: ethers.JsonRpcProvider,
  txHash: string,
  options?: {
    timeoutMs?: number
    pollIntervalMs?: number
    maxNotFoundRetries?: number
  }
): Promise<ethers.TransactionReceipt> {
  const timeoutMs = options?.timeoutMs ?? 60000
  const pollIntervalMs = options?.pollIntervalMs ?? 600
  const maxNotFoundRetries = options?.maxNotFoundRetries ?? 10

  const deadline = Date.now() + timeoutMs
  let notFoundRetries = 0

  while (Date.now() < deadline) {
    const tx = await provider.getTransaction(txHash)
    if (!tx) {
      notFoundRetries += 1
      if (notFoundRetries > maxNotFoundRetries) {
        throw new Error(
          `Transaction not found after ${maxNotFoundRetries} retries: ${txHash}`
        )
      }
      await new Promise((resolve) => setTimeout(resolve, pollIntervalMs))
      continue
    }

    if (!tx.blockNumber) {
      await new Promise((resolve) => setTimeout(resolve, pollIntervalMs))
      continue
    }

    const receipt = await provider.getTransactionReceipt(txHash)
    if (receipt) {
      return receipt
    }

    await new Promise((resolve) => setTimeout(resolve, pollIntervalMs))
  }

  throw new Error(`Timeout waiting for transaction receipt: ${txHash}`)
}

async function signContractTx(
  signer: ethers.Wallet,
  txRequest: ethers.TransactionRequest,
  chainIdOverride?: bigint,
  nonceOverride?: number
): Promise<string> {
  const provider = signer.provider
  if (!provider) {
    throw new Error('Signer is missing a provider')
  }

  const feeData = await provider.getFeeData()
  const maxPriorityFeePerGas =
    feeData.maxPriorityFeePerGas ?? DEFAULT_MAX_PRIORITY_FEE_PER_GAS
  const maxFeePerGas = feeData.maxFeePerGas ?? DEFAULT_MAX_FEE_PER_GAS

  const from = await signer.getAddress()
  const nonce =
    nonceOverride ?? (await provider.getTransactionCount(from, 'pending'))
  const network = await provider.getNetwork()
  const chainId = chainIdOverride ?? network.chainId
  const populated = await signer.populateTransaction({
    ...txRequest,
    chainId,
    nonce,
    type: 2,
    maxFeePerGas,
    maxPriorityFeePerGas,
    from,
  })
  return signer.signTransaction(populated)
}

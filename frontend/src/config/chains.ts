const env = import.meta.env

function requireEnv(name: keyof ImportMetaEnv): string {
  const value = env[name]
  if (!value || typeof value !== 'string' || !value.trim()) {
    throw new Error(`Missing required env var: ${name}`)
  }
  return value.trim()
}

function requireNumber(name: keyof ImportMetaEnv): number {
  const raw = requireEnv(name)
  const parsed = Number(raw)
  if (!Number.isFinite(parsed)) {
    throw new Error(`Invalid number for env var: ${name}`)
  }
  return parsed
}

function normalizePrivateKey(value: string | undefined): string {
  if (!value) {
    return ''
  }
  const trimmed = value.trim()
  if (!trimmed) {
    return ''
  }
  return trimmed.startsWith('0x') ? trimmed : `0x${trimmed}`
}

export const CHAIN_A_ID = requireNumber('VITE_CHAIN_A_ID')
export const CHAIN_B_ID = requireNumber('VITE_CHAIN_B_ID')

export const FLASHBLOCKS_ENABLED = env.VITE_FLASHBLOCKS_ENABLED === 'true'

// Display label for the validator execution client (op-reth by default, op-besu
// when the stack runs with --validator-el op-besu). Used only for diagram labels.
export const EL_CLIENT_LABEL = env.VITE_EL_CLIENT_LABEL?.trim() || 'op-reth'

export const CHAIN_A_BUILDER_RPC = requireEnv('VITE_CHAIN_A_BUILDER_RPC')
export const CHAIN_A_OP_RETH_RPC = requireEnv('VITE_CHAIN_A_OP_RETH_RPC')

export const CHAIN_B_BUILDER_RPC = requireEnv('VITE_CHAIN_B_BUILDER_RPC')
export const CHAIN_B_OP_RETH_RPC = requireEnv('VITE_CHAIN_B_OP_RETH_RPC')

export const CHAIN_A_RPC = FLASHBLOCKS_ENABLED ? CHAIN_A_BUILDER_RPC : CHAIN_A_OP_RETH_RPC
export const CHAIN_B_RPC = FLASHBLOCKS_ENABLED ? CHAIN_B_BUILDER_RPC : CHAIN_B_OP_RETH_RPC

export const SIDECAR_A_URL = requireEnv('VITE_SIDECAR_A_URL')
export const SIDECAR_B_URL = requireEnv('VITE_SIDECAR_B_URL')

const healthApiUrl = env.VITE_HEALTH_API_URL
export const HEALTH_API_URL =
  healthApiUrl === undefined ? 'http://localhost:8090' : healthApiUrl.trim()
export const HEALTH_API_ENABLED = HEALTH_API_URL.length > 0

export const CHAIN_A_BLOCKSCOUT = env.VITE_CHAIN_A_BLOCKSCOUT_URL?.trim() || 'http://localhost:19000'
export const CHAIN_B_BLOCKSCOUT = env.VITE_CHAIN_B_BLOCKSCOUT_URL?.trim() || 'http://localhost:29000'

export const CHAIN_A_BRIDGE_ADDRESS = env.VITE_CHAIN_A_BRIDGE_ADDRESS || ''
export const CHAIN_B_BRIDGE_ADDRESS = env.VITE_CHAIN_B_BRIDGE_ADDRESS || ''

export const CHAIN_A_TOKEN_ADDRESS = env.VITE_CHAIN_A_TOKEN_ADDRESS || ''
export const CHAIN_B_TOKEN_ADDRESS = env.VITE_CHAIN_B_TOKEN_ADDRESS || ''

export const CET_FACTORY_ADDRESS = env.VITE_CET_FACTORY_ADDRESS || ''

export const CHAIN_A_ETH_LIQUIDITY_ADDRESS = env.VITE_CHAIN_A_ETH_LIQUIDITY_ADDRESS || ''
export const CHAIN_B_ETH_LIQUIDITY_ADDRESS = env.VITE_CHAIN_B_ETH_LIQUIDITY_ADDRESS || ''

export const CHAIN_A_PRIVATE_KEY = normalizePrivateKey(
  env.VITE_CHAIN_A_PRIVATE_KEY || env.VITE_WALLET_PRIVATE_KEY
)
export const CHAIN_B_PRIVATE_KEY = normalizePrivateKey(
  env.VITE_CHAIN_B_PRIVATE_KEY || env.VITE_WALLET_PRIVATE_KEY
)

// Bundler (ERC-4337 v0.7). Empty strings disable the Bundler Test tab.
export const BUNDLER_A_URL = env.VITE_BUNDLER_A_URL?.trim() || ''
export const BUNDLER_B_URL = env.VITE_BUNDLER_B_URL?.trim() || ''

export const ENTRYPOINT_A = env.VITE_ENTRYPOINT_A?.trim() || ''
export const ENTRYPOINT_B = env.VITE_ENTRYPOINT_B?.trim() || ''

export const SIMPLE_ACCOUNT_FACTORY_A = env.VITE_SIMPLE_ACCOUNT_FACTORY_A?.trim() || ''
export const SIMPLE_ACCOUNT_FACTORY_B = env.VITE_SIMPLE_ACCOUNT_FACTORY_B?.trim() || ''

export const BUNDLER_TEST_AVAILABLE =
  !!BUNDLER_A_URL && !!ENTRYPOINT_A && !!SIMPLE_ACCOUNT_FACTORY_A

/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_CHAIN_A_ID: string
  readonly VITE_CHAIN_B_ID: string
  readonly VITE_FLASHBLOCKS_ENABLED?: string
  readonly VITE_CHAIN_A_BUILDER_RPC?: string
  readonly VITE_CHAIN_A_OP_RETH_RPC?: string
  readonly VITE_CHAIN_B_BUILDER_RPC?: string
  readonly VITE_CHAIN_B_OP_RETH_RPC?: string
  readonly VITE_CHAIN_A_RPC?: string
  readonly VITE_CHAIN_B_RPC?: string
  readonly VITE_SIDECAR_A_URL: string
  readonly VITE_SIDECAR_B_URL: string
  readonly VITE_HEALTH_API_URL?: string
  readonly VITE_CHAIN_A_BLOCKSCOUT_URL?: string
  readonly VITE_CHAIN_B_BLOCKSCOUT_URL?: string
  readonly VITE_CHAIN_A_BRIDGE_ADDRESS?: string
  readonly VITE_CHAIN_B_BRIDGE_ADDRESS?: string
  readonly VITE_CHAIN_A_TOKEN_ADDRESS?: string
  readonly VITE_CHAIN_B_TOKEN_ADDRESS?: string
  readonly VITE_CET_FACTORY_ADDRESS?: string
  readonly VITE_CHAIN_A_PRIVATE_KEY?: string
  readonly VITE_CHAIN_B_PRIVATE_KEY?: string
  readonly VITE_WALLET_PRIVATE_KEY?: string
  readonly VITE_EL_CLIENT_LABEL?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}

import { defineConfig, loadEnv, type ProxyOptions } from 'vite'
import react from '@vitejs/plugin-react'

function pathProxy(target: string, prefix: string): ProxyOptions {
  return {
    target,
    changeOrigin: true,
    rewrite: (path) => path.replace(new RegExp(`^${prefix}`), '') || '/',
  }
}

function requireStageTarget(env: Record<string, string>, name: string): string {
  const value = env[name]?.trim()
  if (!value) {
    throw new Error(
      `Missing required stage proxy target ${name}. Set it in .env.stage.local or the deployment environment.`
    )
  }
  return value
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  const stageProxy =
    mode === 'stage'
      ? {
          '/stage-rpc/a-builder': pathProxy(
            requireStageTarget(env, 'STAGE_CHAIN_A_BUILDER_RPC_TARGET'),
            '/stage-rpc/a-builder',
          ),
          '/stage-rpc/a-reth': pathProxy(
            requireStageTarget(env, 'STAGE_CHAIN_A_OP_RETH_RPC_TARGET'),
            '/stage-rpc/a-reth',
          ),
          '/stage-rpc/b-builder': pathProxy(
            requireStageTarget(env, 'STAGE_CHAIN_B_BUILDER_RPC_TARGET'),
            '/stage-rpc/b-builder',
          ),
          '/stage-rpc/b-reth': pathProxy(
            requireStageTarget(env, 'STAGE_CHAIN_B_OP_RETH_RPC_TARGET'),
            '/stage-rpc/b-reth',
          ),
          '/stage-sidecar/a': pathProxy(
            requireStageTarget(env, 'STAGE_SIDECAR_A_TARGET'),
            '/stage-sidecar/a',
          ),
          '/stage-sidecar/b': pathProxy(
            requireStageTarget(env, 'STAGE_SIDECAR_B_TARGET'),
            '/stage-sidecar/b',
          ),
        }
      : {}

  return {
    plugins: [react()],
    server: {
      port: 1338,
      proxy: {
        '/api': {
          target: 'http://localhost:17090',
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/api/, ''),
        },
        ...stageProxy,
      },
    },
  }
})

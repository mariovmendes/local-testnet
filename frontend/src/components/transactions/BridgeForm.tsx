import { useEffect, useState } from 'react'
import { ethers } from 'ethers'
import type { FlowMode } from '../visualization/TransactionFlowPanel'
import { useTransactionStore } from '../../stores/transactionStore'
import { submitXT, waitForDecision } from '../../api/sidecar'
import {
  CHAIN_A_ID,
  CHAIN_B_ID,
  buildApproveTx,
  buildBridgeERC20ToTx,
  buildBridgeEthToTx,
  buildBridgeReceiveEthTx,
  buildBridgeReceiveTokensTx,
  buildFundEthLiquidityTx,
  generateSessionId,
  getBridgeAddress,
  getEthLiquidityAddress,
  getEthLiquidityBalance,
  getSigner,
  getTokenAddress,
  parseAmount,
  getProvider,
  waitForTransactionReceipt,
} from '../../api/rollup'
import BalanceDisplay from '../wallet/BalanceDisplay'

interface BridgeFormProps {
  onSubmit: (instanceId: string) => void
  onSelectFlow?: (mode: FlowMode) => void
}

interface EthLiquidityPanelProps {
  seeding: boolean
  setSeeding: (v: boolean) => void
  setError: (msg: string | null) => void
}

function EthLiquidityPanel({ seeding, setSeeding, setError }: EthLiquidityPanelProps) {
  const [balances, setBalances] = useState<{ a: bigint; b: bigint } | null>(null)
  const [seedAmount, setSeedAmount] = useState('1')

  const refresh = async () => {
    try {
      const [a, b] = await Promise.all([
        getEthLiquidityBalance('A'),
        getEthLiquidityBalance('B'),
      ])
      setBalances({ a, b })
    } catch (err) {
      setBalances(null)
      console.warn('Unable to read ETH liquidity balances:', err)
    }
  }

  useEffect(() => {
    refresh()
    const i = setInterval(refresh, 5000)
    return () => clearInterval(i)
  }, [])

  const seedBoth = async () => {
    setError(null)
    setSeeding(true)
    try {
      const value = ethers.parseEther(seedAmount || '0')
      if (value === 0n) throw new Error('Enter a non-zero seed amount')

      const signerA = getSigner('A')
      const signerB = getSigner('B')
      const liqA = getEthLiquidityAddress('A')
      const liqB = getEthLiquidityAddress('B')
      const providerA = getProvider('A')
      const providerB = getProvider('B')
      const senderA = await signerA.getAddress()
      const senderB = await signerB.getAddress()
      const nonceA = await providerA.getTransactionCount(senderA, 'pending')
      const nonceB = await providerB.getTransactionCount(senderB, 'pending')

      const txA = await buildFundEthLiquidityTx(liqA, value, signerA, CHAIN_A_ID, nonceA)
      const txB = await buildFundEthLiquidityTx(liqB, value, signerB, CHAIN_B_ID, nonceB)
      const [resA, resB] = await Promise.all([
        providerA.broadcastTransaction(txA),
        providerB.broadcastTransaction(txB),
      ])
      await Promise.all([
        waitForTransactionReceipt(providerA, resA.hash, { timeoutMs: 30000 }),
        waitForTransactionReceipt(providerB, resB.hash, { timeoutMs: 30000 }),
      ])
      await refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to seed liquidity')
    } finally {
      setSeeding(false)
    }
  }

  const fmt = (b: bigint) => parseFloat(ethers.formatEther(b)).toFixed(4)
  return (
    <div className="bg-bg border border-border px-2.5 py-2 space-y-2">
      <div className="flex items-center justify-between gap-2">
        <span className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim flex-none">
          ETH Pools
        </span>
        <div className="flex items-center gap-3 font-mono text-[11px] text-text-secondary">
          <span>A {balances ? fmt(balances.a) : '-'}</span>
          <span className="text-border-bright">·</span>
          <span>B {balances ? fmt(balances.b) : '-'}</span>
        </div>
      </div>
      <div className="flex gap-2">
        <input
          type="text"
          value={seedAmount}
          onChange={e => setSeedAmount(e.target.value)}
          className="flex-1 bg-bg px-2 py-1.5 border border-border text-text-primary text-xs font-mono focus:outline-none focus:border-amber transition-colors"
          placeholder="1 ETH"
        />
        <button
          type="button"
          onClick={seedBoth}
          disabled={seeding}
          className={`px-2.5 py-1.5 font-display text-[9px] tracking-[0.2em] uppercase border transition-all ${
            seeding
              ? 'border-border text-text-dim cursor-not-allowed'
              : 'border-amber text-amber hover:bg-amber hover:text-bg'
          }`}
        >
          {seeding ? 'Seeding…' : 'Seed Both'}
        </button>
      </div>
    </div>
  )
}

type Direction = 'a_to_b' | 'b_to_a'
type Asset = 'erc20' | 'eth'
const ETH_BRIDGE_SOURCE_GAS_LIMIT = 900000n

export default function BridgeForm({ onSubmit, onSelectFlow }: BridgeFormProps) {
  const [amount, setAmount] = useState('')
  const [direction, setDirection] = useState<Direction>('a_to_b')
  const [asset, setAsset] = useState<Asset>('erc20')
  const [repeatCount, setRepeatCount] = useState('1')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [seeding, setSeeding] = useState(false)

  const { addTransaction, updateTransaction, setFlowStep } = useTransactionStore()

  const sourceChain = direction === 'a_to_b' ? CHAIN_A_ID : CHAIN_B_ID
  const destChain = direction === 'a_to_b' ? CHAIN_B_ID : CHAIN_A_ID

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setError(null)
    setLoading(true)

    try {
      if (!amount) throw new Error('Enter an amount')

      const repeat = Math.max(1, Number.parseInt(repeatCount, 10) || 1)
      const parsedAmount = parseAmount(amount)

      onSelectFlow?.('xt')
      setFlowStep('submitting')

      const signerA = getSigner('A')
      const signerB = getSigner('B')
      const senderA = await signerA.getAddress()
      const senderB = await signerB.getAddress()
      const bridgeA = getBridgeAddress('A')
      const bridgeB = getBridgeAddress('B')
      const tokenA = asset === 'erc20' ? getTokenAddress('A') : ''
      const tokenB = asset === 'erc20' ? getTokenAddress('B') : ''
      const providerA = getProvider('A')
      const providerB = getProvider('B')

      if (asset === 'eth') {
        const source = direction === 'a_to_b' ? 'A' : 'B'
        const sourceProvider = source === 'A' ? providerA : providerB
        const sourceSender = source === 'A' ? senderA : senderB
        const sourceBalance = await sourceProvider.getBalance(sourceSender)
        const needed = parsedAmount * BigInt(repeat)
        const feeData = await sourceProvider.getFeeData()
        const gasPrice =
          feeData.maxFeePerGas ?? feeData.gasPrice ?? 0n
        const gasBudget =
          gasPrice * ETH_BRIDGE_SOURCE_GAS_LIMIT * BigInt(repeat)
        const totalNeeded = needed + gasBudget

        if (sourceBalance < totalNeeded) {
          const maxBridgeable =
            sourceBalance > gasBudget ? sourceBalance - gasBudget : 0n
          throw new Error(
            `Wallet on chain ${source} holds ${ethers.formatEther(sourceBalance)} ETH, but ${
              ethers.formatEther(totalNeeded)
            } ETH is needed for ${repeat} bridgeEthTo tx(s). Try ${
              ethers.formatEther(maxBridgeable)
            } ETH or fund the demo wallet.`
          )
        }

        const destBridge = direction === 'a_to_b' ? 'B' : 'A'
        const destBalance = await getEthLiquidityBalance(destBridge)
        if (destBalance < needed) {
          throw new Error(
            `ETH liquidity pool ${destBridge} holds ${ethers.formatEther(destBalance)} ETH but ${
              ethers.formatEther(needed)
            } ETH is required for receiveETH.mint. Seed the destination pool first (button above).`
          )
        }
      }

      const baseNonceA = await providerA.getTransactionCount(senderA, 'pending')
      const baseNonceB = await providerB.getTransactionCount(senderB, 'pending')

      const trackXT = (
        instanceId: string,
        txHashA: string,
        txHashB: string,
        animateFlow: boolean
      ) => {
        if (animateFlow) {
          const runFlow = async () => {
            setFlowStep('forward_to_peer')
            await new Promise(r => setTimeout(r, 500))
            setFlowStep('lock_builder_a')
            await new Promise(r => setTimeout(r, 300))
            setFlowStep('lock_builder_b')
            await new Promise(r => setTimeout(r, 300))
            setFlowStep('simulating_a')
            await new Promise(r => setTimeout(r, 400))
            setFlowStep('simulating_b')
            await new Promise(r => setTimeout(r, 400))
            setFlowStep('circ_exchange')
            await new Promise(r => setTimeout(r, 500))
            setFlowStep('voting')
            await new Promise(r => setTimeout(r, 600))
          }
          runFlow()
        }

        waitForDecision(instanceId, 60000).then(async (decision) => {
          if (animateFlow) {
            setFlowStep('decided')
            await new Promise(r => setTimeout(r, 300))
          }

          if (decision) {
            if (animateFlow) {
              setFlowStep('delivering')
              await new Promise(r => setTimeout(r, 400))
              setFlowStep('confirming')
            }

            try {
              const [receiptA, receiptB] = await Promise.all([
                waitForTransactionReceipt(providerA, txHashA, { timeoutMs: 30000 }),
                waitForTransactionReceipt(providerB, txHashB, { timeoutMs: 30000 }),
              ])
              updateTransaction(txHashA, {
                status: receiptA.status === 1 ? 'committed' : 'aborted',
                decision: receiptA.status === 1,
                decidedAt: new Date(),
              })
              updateTransaction(txHashB, {
                status: receiptB.status === 1 ? 'committed' : 'aborted',
                decision: receiptB.status === 1,
                decidedAt: new Date(),
              })
              if (animateFlow) {
                setFlowStep('complete')
                setTimeout(() => setFlowStep('idle'), 1000)
              }
            } catch (err) {
              console.error('Error waiting for receipts:', err)
              updateTransaction(txHashA, { status: 'aborted', decision: false, decidedAt: new Date() })
              updateTransaction(txHashB, { status: 'aborted', decision: false, decidedAt: new Date() })
              if (animateFlow) setFlowStep('idle')
            }
          } else {
            updateTransaction(txHashA, { status: 'aborted', decision: false, decidedAt: new Date() })
            updateTransaction(txHashB, { status: 'aborted', decision: false, decidedAt: new Date() })
            if (animateFlow) setFlowStep('idle')
          }
        }).catch(err => {
          console.error('Error waiting for decision:', err)
          updateTransaction(txHashA, { status: 'aborted', decision: false, decidedAt: new Date() })
          updateTransaction(txHashB, { status: 'aborted', decision: false, decidedAt: new Date() })
          if (animateFlow) setFlowStep('idle')
        })
      }

      const sourceNonceStride = asset === 'erc20' ? 2 : 1

      for (let i = 0; i < repeat; i += 1) {
        const sessionId = generateSessionId()
        const nonceA =
          direction === 'a_to_b' ? baseNonceA + i * sourceNonceStride : baseNonceA + i
        const nonceB =
          direction === 'a_to_b' ? baseNonceB + i : baseNonceB + i * sourceNonceStride
        const transactions: Record<number, string[]> = {}
        let txABytes: string
        let txBBytes: string

        if (asset === 'erc20') {
          if (direction === 'a_to_b') {
            const txAApprove = await buildApproveTx(tokenA, bridgeA, parsedAmount, signerA, CHAIN_A_ID, nonceA)
            const txABridge = await buildBridgeERC20ToTx(bridgeA, CHAIN_B_ID, tokenA, parsedAmount, senderB, sessionId, signerA, CHAIN_A_ID, nonceA + 1)
            const txBReceive = await buildBridgeReceiveTokensTx(bridgeB, sessionId, CHAIN_A_ID, CHAIN_B_ID, bridgeA, senderB, signerB, CHAIN_B_ID, nonceB)
            transactions[CHAIN_A_ID] = [txAApprove, txABridge]
            transactions[CHAIN_B_ID] = [txBReceive]
            txABytes = txABridge
            txBBytes = txBReceive
          } else {
            const txBApprove = await buildApproveTx(tokenB, bridgeB, parsedAmount, signerB, CHAIN_B_ID, nonceB)
            const txBBridge = await buildBridgeERC20ToTx(bridgeB, CHAIN_A_ID, tokenB, parsedAmount, senderA, sessionId, signerB, CHAIN_B_ID, nonceB + 1)
            const txAReceive = await buildBridgeReceiveTokensTx(bridgeA, sessionId, CHAIN_B_ID, CHAIN_A_ID, bridgeB, senderA, signerA, CHAIN_A_ID, nonceA)
            transactions[CHAIN_B_ID] = [txBApprove, txBBridge]
            transactions[CHAIN_A_ID] = [txAReceive]
            txABytes = txAReceive
            txBBytes = txBBridge
          }
        } else {
          if (direction === 'a_to_b') {
            const txABridge = await buildBridgeEthToTx(bridgeA, CHAIN_B_ID, senderB, sessionId, parsedAmount, signerA, CHAIN_A_ID, nonceA)
            const txBReceive = await buildBridgeReceiveEthTx(bridgeB, sessionId, CHAIN_A_ID, CHAIN_B_ID, bridgeA, senderB, signerB, CHAIN_B_ID, nonceB)
            transactions[CHAIN_A_ID] = [txABridge]
            transactions[CHAIN_B_ID] = [txBReceive]
            txABytes = txABridge
            txBBytes = txBReceive
          } else {
            const txBBridge = await buildBridgeEthToTx(bridgeB, CHAIN_A_ID, senderA, sessionId, parsedAmount, signerB, CHAIN_B_ID, nonceB)
            const txAReceive = await buildBridgeReceiveEthTx(bridgeA, sessionId, CHAIN_B_ID, CHAIN_A_ID, bridgeB, senderA, signerA, CHAIN_A_ID, nonceA)
            transactions[CHAIN_B_ID] = [txBBridge]
            transactions[CHAIN_A_ID] = [txAReceive]
            txABytes = txAReceive
            txBBytes = txBBridge
          }
        }

        const txHashA = ethers.Transaction.from(txABytes).hash!
        const txHashB = ethers.Transaction.from(txBBytes).hash!

        const response = await submitXT(transactions)
        const instanceId = response.instance_id

        addTransaction({ instanceId: txHashA, type: 'bridge', status: 'pending', chainId: CHAIN_A_ID, createdAt: new Date() })
        addTransaction({ instanceId: txHashB, type: 'bridge', status: 'pending', chainId: CHAIN_B_ID, createdAt: new Date() })

        trackXT(instanceId, txHashA, txHashB, repeat === 1)
        if (repeat === 1) onSubmit(instanceId)
      }

      if (repeat > 1) setFlowStep('idle')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error')
      setFlowStep('idle')
    } finally {
      setLoading(false)
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-3">
      <div className="grid grid-cols-2 gap-2">
        <div className="bg-bg border border-border px-2.5 py-2 flex items-center justify-between">
          <span className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim">
            Chain A
          </span>
          <BalanceDisplay chain="A" remoteChain="B" />
        </div>
        <div className="bg-bg border border-border px-2.5 py-2 flex items-center justify-between">
          <span className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim">
            Chain B
          </span>
          <BalanceDisplay chain="B" remoteChain="A" />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <ToggleGroup
          label="Asset"
          value={asset}
          onChange={setAsset}
          options={[{ id: 'erc20', label: 'ERC20' }, { id: 'eth', label: 'ETH' }]}
        />
        <ToggleGroup
          label="Direction"
          value={direction}
          onChange={setDirection}
          options={[{ id: 'a_to_b', label: 'A → B' }, { id: 'b_to_a', label: 'B → A' }]}
        />
      </div>

      {asset === 'eth' && (
        <EthLiquidityPanel seeding={seeding} setSeeding={setSeeding} setError={setError} />
      )}

      <div className="grid grid-cols-[1fr_92px] gap-2">
        <div>
          <label className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim block mb-1">
            Amount
          </label>
          <div className="relative">
            <input
              type="text"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              placeholder="0.000"
              className="w-full bg-bg px-2.5 py-2 border border-border text-text-primary text-sm font-mono
                placeholder:text-text-dim focus:outline-none focus:border-amber transition-colors pr-12"
            />
            <span className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[9px] text-text-dim font-display tracking-widest uppercase">
              {asset === 'erc20' ? 'tkn' : 'eth'}
            </span>
          </div>
        </div>
        <div>
          <label className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim block mb-1">
            Repeat
          </label>
          <input
            type="number"
            min={1}
            value={repeatCount}
            onChange={(e) => setRepeatCount(e.target.value)}
            className="w-full bg-bg px-2.5 py-2 border border-border text-text-primary text-sm font-mono
              focus:outline-none focus:border-amber transition-colors"
          />
        </div>
      </div>

      {error && (
        <div className="border border-error/40 bg-error/5 px-2.5 py-2 text-error text-[10px] font-mono leading-snug">
          <span className="text-error/60 mr-1.5">!</span>{error}
        </div>
      )}

      <button
        type="submit"
        disabled={loading || !amount}
        className={`w-full py-2.5 font-display text-[11px] tracking-[0.3em] uppercase border transition-all ${
          loading || !amount
            ? 'border-border text-text-dim cursor-not-allowed'
            : 'border-amber text-amber hover:bg-amber hover:text-bg glow-amber'
        }`}
      >
        {loading ? (
          <span className="flex items-center justify-center gap-2">
            <span className="w-1 h-1 bg-current rounded-full indicator-active" />
            <span className="w-1 h-1 bg-current rounded-full indicator-active" style={{ animationDelay: '0.3s' }} />
            <span className="w-1 h-1 bg-current rounded-full indicator-active" style={{ animationDelay: '0.6s' }} />
            <span className="ml-2">Coordinating</span>
          </span>
        ) : (
          `Submit XT  ·  Chain ${sourceChain} → Chain ${destChain}`
        )}
      </button>
    </form>
  )
}

interface ToggleOption<T extends string> {
  id: T
  label: string
}

interface ToggleGroupProps<T extends string> {
  label: string
  value: T
  onChange: (v: T) => void
  options: ToggleOption<T>[]
}

function ToggleGroup<T extends string>({ label, value, onChange, options }: ToggleGroupProps<T>) {
  return (
    <div>
      <label className="font-display text-[9px] tracking-[0.25em] uppercase text-text-dim block mb-1">
        {label}
      </label>
      <div className="grid grid-cols-2 gap-1.5">
        {options.map(opt => (
          <button
            key={opt.id}
            type="button"
            onClick={() => onChange(opt.id)}
            className={`py-2 font-display text-[10px] tracking-[0.2em] uppercase border transition-all ${
              value === opt.id
                ? 'border-amber text-amber bg-amber/5'
                : 'border-border text-text-secondary hover:border-border-bright hover:text-text-primary'
            }`}
          >
            {opt.label}
          </button>
        ))}
      </div>
    </div>
  )
}

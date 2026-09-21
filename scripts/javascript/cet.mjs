// Resolves the CET (the wrapped chain-A token) on chain B and reads balances.
//
// The CET is not deployed with the rest of the stack: CetFactory.deployIfAbsent
// creates it lazily on the FIRST successful delivery, at a CREATE2 address
// derived from (originToken, originChainId). So before any XT completes there is
// no contract at that address and every balanceOf returns "0x" -> ethers
// BAD_DATA. Treat "no code" as balance 0: that is what it means.
import { ethers } from 'ethers'
import fs from 'node:fs'
import path from 'node:path'

const NETWORKS = path.join(import.meta.dirname, '../../.localnet/networks')

export const addresses = chain =>
  JSON.parse(fs.readFileSync(path.join(NETWORKS, chain, 'contracts.json'), 'utf8')).addresses

// balanceOf for the CET on chain B, or () => 0n while the CET does not exist yet.
export async function cetBalanceOf(pb, tokenA, chainIdA) {
  const factory = new ethers.Contract(addresses('rollup-b').CetFactory,
    ['function predictAddress(address,uint256) view returns (address)'], pb)
  const cet = await factory.predictAddress(tokenA, chainIdA)
  if (await pb.getCode(cet) === '0x') {
    console.error(`  note: CET ${cet} not deployed on B yet -> 0 deliveries so far`)
    return { cet, balanceOf: async () => 0n }
  }
  return { cet, balanceOf: new ethers.Contract(cet, ['function balanceOf(address) view returns (uint256)'], pb).balanceOf }
}

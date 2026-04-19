import { useContext } from "react"
import { TProviderManager } from "@/types"
import { DevsyContext, TDevsyContext } from "./DevsyProvider"
import { useProviderManager } from "./useProviderManager"

export function useProviders(): [TDevsyContext["providers"] | [undefined], TProviderManager] {
  const providers = useContext(DevsyContext)?.providers ?? [undefined]
  const manager = useProviderManager()

  return [providers, manager]
}

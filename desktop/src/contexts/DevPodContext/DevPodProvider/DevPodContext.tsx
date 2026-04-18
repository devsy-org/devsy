import { createContext } from "react"
import { TProviders, TQueryResult } from "@/types"

export type TDevsyContext = Readonly<{
  providers: TQueryResult<TProviders>
}>
export const DevsyContext = createContext<TDevsyContext | null>(null)

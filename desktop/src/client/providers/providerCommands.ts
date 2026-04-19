import { exists, getErrorFromChildProcess, Result, ResultError, Return } from "@/lib"
import {
  TAddProviderConfig,
  TCheckProviderUpdateResult,
  TProviderID,
  TProviderOptions,
  TProviders,
  TProviderSource,
} from "@/types"
import { Command, isOk, serializeRawOptions, toFlagArg } from "../command"
import {
  DEVSY_COMMAND_ADD,
  DEVSY_COMMAND_DELETE,
  DEVSY_COMMAND_GET_PROVIDER_NAME,
  DEVSY_COMMAND_LIST,
  DEVSY_COMMAND_OPTIONS,
  DEVSY_COMMAND_PROVIDER,
  DEVSY_COMMAND_RENAME,
  DEVSY_COMMAND_SET_OPTIONS,
  DEVSY_COMMAND_UPDATE,
  DEVSY_COMMAND_USE,
  DEVSY_FLAG_DEBUG,
  DEVSY_FLAG_DRY,
  DEVSY_FLAG_JSON_LOG_OUTPUT,
  DEVSY_FLAG_JSON_OUTPUT,
  DEVSY_FLAG_NAME,
  DEVSY_FLAG_RECONFIGURE,
  DEVSY_FLAG_SINGLE_MACHINE,
  DEVSY_FLAG_USE,
} from "../constants"
import { DEVSY_COMMAND_CHECK_PROVIDER_UPDATE, DEVSY_COMMAND_HELPER } from "./../constants"

export class ProviderCommands {
  static DEBUG = false

  private static newCommand(args: string[]): Command {
    return new Command([...args, ...(ProviderCommands.DEBUG ? [DEVSY_FLAG_DEBUG] : [])])
  }

  static async ListProviders(): Promise<Result<TProviders>> {
    const result = await new Command([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_LIST,
      DEVSY_FLAG_JSON_OUTPUT,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    const rawProviders = JSON.parse(result.val.stdout) as TProviders
    for (const provider of Object.values(rawProviders)) {
      provider.isProxyProvider =
        provider.config?.exec?.proxy !== undefined || provider.config?.exec?.daemon !== undefined
    }

    return Return.Value(rawProviders)
  }

  static async GetProviderID(source: string) {
    const result = await new Command([
      DEVSY_COMMAND_HELPER,
      DEVSY_COMMAND_GET_PROVIDER_NAME,
      source,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(result.val.stdout)
  }

  static async AddProvider(
    rawProviderSource: string,
    config: TAddProviderConfig
  ): Promise<ResultError> {
    const maybeName = config.name
    const maybeNameFlag = exists(maybeName) ? [toFlagArg(DEVSY_FLAG_NAME, maybeName)] : []
    const useFlag = toFlagArg(DEVSY_FLAG_USE, "false")

    const result = await ProviderCommands.newCommand([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_ADD,
      rawProviderSource,
      ...maybeNameFlag,
      useFlag,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Ok()
  }

  static async RemoveProvider(id: TProviderID) {
    const result = await ProviderCommands.newCommand([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_DELETE,
      id,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Ok()
  }

  static async UseProvider(
    id: TProviderID,
    rawOptions?: Record<string, unknown>,
    reuseMachine?: boolean
  ) {
    const optionsFlag = rawOptions ? serializeRawOptions(rawOptions) : []
    const maybeResuseMachineFlag = reuseMachine ? [DEVSY_FLAG_SINGLE_MACHINE] : []

    const result = await ProviderCommands.newCommand([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_USE,
      id,
      ...optionsFlag,
      ...maybeResuseMachineFlag,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()

    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Ok()
  }

  static async SetProviderOptions(
    id: TProviderID,
    rawOptions: Record<string, unknown>,
    reuseMachine: boolean,
    dry?: boolean,
    reconfigure?: boolean
  ) {
    const optionsFlag = serializeRawOptions(rawOptions)
    const maybeResuseMachineFlag = reuseMachine ? [DEVSY_FLAG_SINGLE_MACHINE] : []
    const maybeDry = dry ? [DEVSY_FLAG_DRY] : []
    const maybeReconfigure = reconfigure ? [DEVSY_FLAG_RECONFIGURE] : []

    const result = await ProviderCommands.newCommand([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_SET_OPTIONS,
      id,
      ...optionsFlag,
      ...maybeResuseMachineFlag,
      ...maybeDry,
      ...maybeReconfigure,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    } else if (dry) {
      return Return.Value(JSON.parse(result.val.stdout) as TProviderOptions)
    }

    return Return.Ok()
  }

  static async GetProviderOptions(id: TProviderID) {
    const result = await new Command([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_OPTIONS,
      id,
      DEVSY_FLAG_JSON_OUTPUT,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TProviderOptions)
  }

  static async CheckProviderUpdate(id: TProviderID) {
    const result = await new Command([
      DEVSY_COMMAND_HELPER,
      DEVSY_COMMAND_CHECK_PROVIDER_UPDATE,
      id,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TCheckProviderUpdateResult)
  }

  static async UpdateProvider(id: TProviderID, source: TProviderSource) {
    const useFlag = toFlagArg(DEVSY_FLAG_USE, "false")

    const result = await new Command([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_UPDATE,
      id,
      source.raw ?? source.github ?? source.url ?? source.file ?? "",
      DEVSY_FLAG_JSON_LOG_OUTPUT,
      useFlag,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Ok()
  }

  static async RenameProvider(oldName: TProviderID, newName: string) {
    const result = await ProviderCommands.newCommand([
      DEVSY_COMMAND_PROVIDER,
      DEVSY_COMMAND_RENAME,
      oldName,
      newName,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Ok()
  }
}

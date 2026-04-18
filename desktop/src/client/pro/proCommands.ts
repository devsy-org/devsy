import { Result, ResultError, Return, getErrorFromChildProcess } from "@/lib"
import {
  TImportWorkspaceConfig,
  TListProInstancesConfig,
  TPlatformHealthCheck,
  TProID,
  TProInstance,
  TPlatformVersionInfo,
  TPlatformUpdateCheck,
} from "@/types"
import { Command, isOk, serializeRawOptions, toFlagArg } from "../command"
import {
  DEVSY_COMMAND_DELETE,
  DEVSY_COMMAND_IMPORT_WORKSPACE,
  DEVSY_COMMAND_LIST,
  DEVSY_COMMAND_LOGIN,
  DEVSY_COMMAND_PRO,
  DEVSY_FLAG_ACCESS_KEY,
  DEVSY_FLAG_DEBUG,
  DEVSY_FLAG_FORCE_BROWSER,
  DEVSY_FLAG_HOST,
  DEVSY_FLAG_INSTANCE,
  DEVSY_FLAG_JSON_LOG_OUTPUT,
  DEVSY_FLAG_JSON_OUTPUT,
  DEVSY_FLAG_LOGIN,
  DEVSY_FLAG_PROJECT,
  DEVSY_FLAG_USE,
  DEVSY_FLAG_WORKSPACE_ID,
  DEVSY_FLAG_WORKSPACE_PROJECT,
  DEVSY_FLAG_WORKSPACE_UID,
} from "../constants"
import { TStreamEventListenerFn } from "../types"
import { ManagementV1DevsyWorkspaceInstance } from "@devsy/client/gen/models/managementV1DevsyWorkspaceInstance"
import { ManagementV1Project } from "@devsy/client/gen/models/managementV1Project"
import { ManagementV1Self } from "@devsy/client/gen/models/managementV1Self"
import { ManagementV1ProjectTemplates } from "@devsy/client/gen/models/managementV1ProjectTemplates"
import { ManagementV1ProjectClusters } from "@devsy/client/gen/models/managementV1ProjectClusters"

export class ProCommands {
  static DEBUG = false

  private static newCommand(args: string[]): Command {
    return new Command([...args, ...(ProCommands.DEBUG ? [DEVSY_FLAG_DEBUG] : [])])
  }

  static async Login(
    host: string,
    accessKey?: string,
    listener?: TStreamEventListenerFn
  ): Promise<ResultError> {
    const maybeAccessKeyFlag = accessKey ? [toFlagArg(DEVSY_FLAG_ACCESS_KEY, accessKey)] : []
    const useFlag = toFlagArg(DEVSY_FLAG_USE, "false")

    const cmd = ProCommands.newCommand([
      DEVSY_COMMAND_PRO,
      DEVSY_COMMAND_LOGIN,
      host,
      useFlag,
      DEVSY_FLAG_FORCE_BROWSER,
      DEVSY_FLAG_JSON_LOG_OUTPUT,
      ...maybeAccessKeyFlag,
    ])
    if (listener) {
      return cmd.stream(listener)
    } else {
      const result = await cmd.run()
      if (result.err) {
        return result
      }

      if (!isOk(result.val)) {
        return getErrorFromChildProcess(result.val)
      }

      return Return.Ok()
    }
  }

  static async ListProInstances(
    config?: TListProInstancesConfig
  ): Promise<Result<readonly TProInstance[]>> {
    const maybeLoginFlag = config?.authenticate ? [DEVSY_FLAG_LOGIN] : []
    const result = await ProCommands.newCommand([
      DEVSY_COMMAND_PRO,
      DEVSY_COMMAND_LIST,
      DEVSY_FLAG_JSON_OUTPUT,
      ...maybeLoginFlag,
    ]).run()
    if (result.err) {
      return result
    }

    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    const instances = JSON.parse(result.val.stdout) as readonly TProInstance[]

    return Return.Value(instances)
  }

  static async RemoveProInstance(id: TProID) {
    const result = await ProCommands.newCommand([
      DEVSY_COMMAND_PRO,
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

  static async ImportWorkspace(config: TImportWorkspaceConfig): Promise<ResultError> {
    const optionsFlag = config.options ? serializeRawOptions(config.options) : []
    const result = await new Command([
      DEVSY_COMMAND_PRO,
      DEVSY_COMMAND_IMPORT_WORKSPACE,
      config.devsyProHost,
      DEVSY_FLAG_WORKSPACE_ID,
      config.workspaceID,
      DEVSY_FLAG_WORKSPACE_UID,
      config.workspaceUID,
      DEVSY_FLAG_WORKSPACE_PROJECT,
      config.project,
      ...optionsFlag,
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

  static WatchWorkspaces(id: TProID, projectName: string) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const projectFlag = toFlagArg(DEVSY_FLAG_PROJECT, projectName)
    const args = [DEVSY_COMMAND_PRO, "watch-workspaces", hostFlag, projectFlag]

    return ProCommands.newCommand(args)
  }

  static async ListProjects(id: TProID) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "list-projects", hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as readonly ManagementV1Project[])
  }

  static async GetSelf(id: TProID) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "self", hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as ManagementV1Self)
  }

  static async ListTemplates(id: TProID, projectName: string) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const projectFlag = toFlagArg(DEVSY_FLAG_PROJECT, projectName)
    const args = [DEVSY_COMMAND_PRO, "list-templates", hostFlag, projectFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as ManagementV1ProjectTemplates)
  }

  static async ListClusters(id: TProID, projectName: string) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const projectFlag = toFlagArg(DEVSY_FLAG_PROJECT, projectName)
    const args = [DEVSY_COMMAND_PRO, "list-clusters", hostFlag, projectFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as ManagementV1ProjectClusters)
  }

  static async CreateWorkspace(id: TProID, instance: ManagementV1DevsyWorkspaceInstance) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const instanceFlag = toFlagArg(DEVSY_FLAG_INSTANCE, JSON.stringify(instance))
    const args = [DEVSY_COMMAND_PRO, "create-workspace", hostFlag, instanceFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as ManagementV1DevsyWorkspaceInstance)
  }

  static async UpdateWorkspace(id: TProID, instance: ManagementV1DevsyWorkspaceInstance) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const instanceFlag = toFlagArg(DEVSY_FLAG_INSTANCE, JSON.stringify(instance))
    const args = [DEVSY_COMMAND_PRO, "update-workspace", hostFlag, instanceFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as ManagementV1DevsyWorkspaceInstance)
  }

  static async CheckHealth(id: TProID) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "check-health", hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TPlatformHealthCheck)
  }

  static async GetVersion(id: TProID) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "version", hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TPlatformVersionInfo)
  }

  static async CheckUpdate(id: TProID) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "check-update", hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TPlatformUpdateCheck)
  }

  static async Update(id: TProID, version: string) {
    const hostFlag = toFlagArg(DEVSY_FLAG_HOST, id)
    const args = [DEVSY_COMMAND_PRO, "update-provider", version, hostFlag]

    const result = await ProCommands.newCommand(args).run()
    if (result.err) {
      return result
    }
    if (!isOk(result.val)) {
      return getErrorFromChildProcess(result.val)
    }

    return Return.Value(JSON.parse(result.val.stdout) as TPlatformUpdateCheck)
  }
}

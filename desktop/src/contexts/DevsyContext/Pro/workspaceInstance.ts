import { TIDE, TIdentifiable, TWorkspaceSource } from "@/types"
import { ManagementV1DevsyWorkspaceInstance } from "@devsy/client/gen/models/managementV1DevsyWorkspaceInstance"
import { Labels, deepCopy } from "@/lib"
import { Resources } from "@devsy/client"
import { ManagementV1DevsyWorkspaceInstanceStatus } from "@devsy/client/gen/models/managementV1DevsyWorkspaceInstanceStatus"

export class ProWorkspaceInstance
  extends ManagementV1DevsyWorkspaceInstance
  implements TIdentifiable
{
  public readonly status: ProWorkspaceInstanceStatus | undefined

  public get id(): string {
    const maybeID = this.metadata?.labels?.[Labels.WorkspaceID]
    if (!maybeID) {
      // If we don't have an ID we should ignore the instance.
      // Throwing an error for now to see how often this happens
      throw new Error(`No Workspace ID label present on instance ${this.metadata?.name}`)
    }

    return maybeID
  }

  constructor(instance: ManagementV1DevsyWorkspaceInstance) {
    super()

    this.apiVersion = `${Resources.ManagementV1DevsyWorkspaceInstance.group}/${Resources.ManagementV1DevsyWorkspaceInstance.version}`
    this.kind = Resources.ManagementV1DevsyWorkspaceInstance.kind
    this.metadata = deepCopy(instance.metadata)
    this.spec = deepCopy(instance.spec)
    this.status = deepCopy(instance.status) as ProWorkspaceInstanceStatus
  }
}

class ProWorkspaceInstanceStatus extends ManagementV1DevsyWorkspaceInstanceStatus {
  "source"?: TWorkspaceSource
  "ide"?: TIDE
  "metrics"?: ProWorkspaceMetricsSummary

  constructor() {
    super()
  }
}

class ProWorkspaceMetricsSummary {
  "latencyMs"?: number
  "connectionType"?: "direct" | "DERP"
  "derpRegion"?: string
}

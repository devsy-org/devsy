import { ProWorkspaceInstance } from "@/contexts"
import { TWorkspaceResult } from "@/contexts/DevsyContext/workspaces/useWorkspace"
import { ManagementV1DevsyWorkspaceTemplate } from "@devsy/client/gen/models/managementV1DevsyWorkspaceTemplate"

export type TTabProps = Readonly<{
  host: string
  workspace: TWorkspaceResult<ProWorkspaceInstance>
  instance: ProWorkspaceInstance
  template: ManagementV1DevsyWorkspaceTemplate | undefined
}>

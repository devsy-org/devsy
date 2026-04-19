import { useProContext } from "@/contexts"
import { useQuery, UseQueryResult } from "@tanstack/react-query"
import { QueryKeys } from "@/queryKeys"
import { ManagementV1DevsyWorkspaceTemplate } from "@devsy/client/gen/models/managementV1DevsyWorkspaceTemplate"
import { ManagementV1DevsyEnvironmentTemplate } from "@devsy/client/gen/models/managementV1DevsyEnvironmentTemplate"
import { ManagementV1DevsyWorkspacePreset } from "@devsy/client/gen/models/managementV1DevsyWorkspacePreset"

type TTemplates = Readonly<{
  default: ManagementV1DevsyWorkspaceTemplate | undefined
  workspace: readonly ManagementV1DevsyWorkspaceTemplate[]
  environment: readonly ManagementV1DevsyEnvironmentTemplate[]
  presets: readonly ManagementV1DevsyWorkspacePreset[]
}>
export function useTemplates(): UseQueryResult<TTemplates> {
  const { host, currentProject, client } = useProContext()
  const query = useQuery<TTemplates>({
    queryKey: QueryKeys.proWorkspaceTemplates(host, currentProject?.metadata!.name!),
    queryFn: async () => {
      const projectTemplates = (
        await client.getProjectTemplates(currentProject?.metadata!.name!)
      ).unwrap()

      // try to find default template in list
      let defaultTemplate: ManagementV1DevsyWorkspaceTemplate | undefined = undefined
      if (projectTemplates?.defaultDevsyWorkspaceTemplate) {
        defaultTemplate = projectTemplates.devsyWorkspaceTemplates?.find(
          (template) => template.metadata?.name === projectTemplates.defaultDevsyWorkspaceTemplate
        )
      }

      return {
        default: defaultTemplate,
        workspace: projectTemplates?.devsyWorkspaceTemplates ?? [],
        environment: projectTemplates?.devsyEnvironmentTemplates ?? [],
        presets: projectTemplates?.devsyWorkspacePresets ?? [],
      }
    },
    enabled: !!currentProject,
  })

  return query
}

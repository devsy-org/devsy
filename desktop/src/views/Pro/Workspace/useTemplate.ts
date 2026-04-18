import { useTemplates } from "@/contexts"
import { TParameterWithValue, getParametersWithValues } from "@/lib"
import { ManagementV1DevsyWorkspaceInstance } from "@devsy/client/gen/models/managementV1DevsyWorkspaceInstance"
import { ManagementV1DevsyWorkspaceTemplate } from "@devsy/client/gen/models/managementV1DevsyWorkspaceTemplate"
import { useMemo } from "react"

export function useTemplate(instance: ManagementV1DevsyWorkspaceInstance | undefined) {
  const { data: templates } = useTemplates()

  return useMemo<{
    parameters: readonly TParameterWithValue[]
    template: ManagementV1DevsyWorkspaceTemplate | undefined
  }>(() => {
    // find template for workspace
    const currentTemplate = templates?.workspace.find(
      (template) => instance?.spec?.templateRef?.name === template.metadata?.name
    )
    const empty = { parameters: [], template: undefined }
    if (!currentTemplate || !instance) {
      return empty
    }

    const parameters = getParametersWithValues(instance, currentTemplate)
    if (!parameters) {
      return { parameters: [], template: currentTemplate }
    }

    return { parameters, template: currentTemplate }
  }, [instance, templates])
}

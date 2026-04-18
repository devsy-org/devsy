import { ManagementV1DevsyWorkspacePreset } from "@devsy/client/gen/models/managementV1DevsyWorkspacePreset"

export function presetDisplayName(preset: ManagementV1DevsyWorkspacePreset | undefined) {
  return preset?.spec?.displayName ?? preset?.metadata?.name
}

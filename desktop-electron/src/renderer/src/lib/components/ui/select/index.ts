import { Select as SelectPrimitive } from "bits-ui"
import Content from "./select-content.svelte"
import Item from "./select-item.svelte"
import Trigger from "./select-trigger.svelte"

const Root = SelectPrimitive.Root
const Group = SelectPrimitive.Group
const GroupHeading = SelectPrimitive.GroupHeading

export {
  Content,
  Content as SelectContent,
  Group,
  Group as SelectGroup,
  GroupHeading,
  GroupHeading as SelectGroupHeading,
  GroupHeading as Label,
  GroupHeading as SelectLabel,
  Item,
  Item as SelectItem,
  Root,
  Root as Select,
  Trigger,
  Trigger as SelectTrigger,
}

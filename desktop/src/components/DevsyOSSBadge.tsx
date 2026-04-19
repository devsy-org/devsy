import { Link, Text } from "@chakra-ui/react"
import { client } from "@/client/client"
import { GITHUB_REPO_URL } from "@/client/repo"

export function DevsyOSSBadge() {
  return (
    <Link
      display="flex"
      alignItems="center"
      justifyContent="start"
      onClick={() => client.openUrl(GITHUB_REPO_URL)}>
      <Text fontSize="sm" variant="muted" marginRight="2">
        Made by Devsy
      </Text>
    </Link>
  )
}

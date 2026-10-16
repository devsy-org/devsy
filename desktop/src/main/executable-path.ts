import { posix } from "node:path"

export function desktopExecutablePath(
  currentPath: string,
  platform: NodeJS.Platform,
  home: string,
): string {
  if (platform !== "darwin") return currentPath

  const extraDirs = [
    "/usr/local/bin",
    "/opt/homebrew/bin",
    "/opt/homebrew/sbin",
    posix.join(home, ".local", "bin"),
  ]
  return [...new Set([...extraDirs, ...currentPath.split(":")])]
    .filter((directory) => directory !== "")
    .join(":")
}

// Package exitcode defines process exit codes used as a contract between
// devsy and any parent process that spawns it as a subprocess (e.g. the
// backhaul SSH command). Keeping these in a leaf package avoids import
// cycles between the packages that emit and consume them.
package exitcode

// WorkspaceNotFound is emitted when devsy exits because the requested
// workspace could not be located. Parent processes may treat this as a
// transient signal (e.g. during a workspace-registration race) and retry.
const WorkspaceNotFound = 75

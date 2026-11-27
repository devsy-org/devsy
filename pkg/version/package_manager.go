package version

// packageManager is injected at build time by package-managed distributions.
// An empty value means the binary owns its update lifecycle.
var packageManager string

// GetPackageManager identifies the package manager responsible for this binary.
func GetPackageManager() string {
	return packageManager
}

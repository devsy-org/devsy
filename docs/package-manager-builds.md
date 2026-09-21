# Package-managed builds

Devsy normally owns its update lifecycle, so `devsy update` downloads and installs a release directly. A package manager that owns the installed binary must set its identity at build time instead:

```sh
go build -ldflags "-X github.com/devsy-org/devsy/pkg/version.packageManager=homebrew" .
```

The `homebrew` identity makes `devsy update` stop before contacting the release service and direct the user to `brew upgrade devsy`. Builds without the marker, including the release binaries used by the current custom tap, keep direct self-update behavior.

To add another package manager:

1. Choose a stable lowercase identity and inject it with the same linker variable.
2. Add the package manager's update command to `cmd/update`.
3. Test that the managed build returns guidance without starting self-update.
4. Add a functional package test that exercises the guidance from the installed binary.

`Formula/devsy.rb` is a source-built candidate for eventual submission to `homebrew/core`. It is not published there by this repository.

Before submitting the candidate formula, replace its release URL and checksum with the first release that contains the package-manager contract. The checked-in placeholder prevents accidentally submitting a formula for an older source tree that cannot honor the managed-update behavior.

// Package runenv is the execution environment shared by the E2E CI helpers.
//
// It owns every side effect the helpers need, as small interfaces bundled in
// Dependencies: running subprocesses (Runner), the filesystem (FileStore),
// time (Clock), TCP dials (Dialer) and HTTPS downloads (Downloader). Command
// logic written against Dependencies is unit-testable with the fakes in
// runenvtest. The package also defines the typed StageError and its stable
// Code, so failures are greppable and map to a process exit status.
//
// runenv imports nothing from the other E2E helper packages.
package runenv

package docker

import "io"

// launchSpec is the fully resolved description of one dial-stdio child: the
// trusted provider-selected executable, the fixed argument vector (without
// argv[0]) and a snapshot of the invocation environment.
type launchSpec struct {
	path string
	args []string
	env  []string
}

// dialProcess owns one launched child and every descendant it spawns.
//
// Wait has exactly one caller (stdioConn.reap). KillTree is idempotent, safe to
// call concurrently with Wait, and a no-op once the child has been reaped.
// Release is called once, after Wait returns, and frees the OS handles that
// back the ownership; it never races with an in-flight KillTree.
type dialProcess interface {
	PID() int
	Wait() error
	KillTree() error
	Release() error
}

// launchedStdio is a started child plus the parent ends of its three streams.
type launchedStdio struct {
	proc   dialProcess
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

// dialLauncher starts a child with process-tree ownership established before
// the child's first instruction runs, or fails without leaving it running.
type dialLauncher func(spec launchSpec) (*launchedStdio, error)

func (i *DockerInvocation) launchSpec() launchSpec {
	return launchSpec{
		path: i.DockerCmd,
		args: []string{"system", "dial-stdio"},
		env:  i.Environ(),
	}
}

func closeAll(closers ...io.Closer) {
	for _, c := range closers {
		if c != nil {
			_ = c.Close()
		}
	}
}

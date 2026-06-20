package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
)

func extractBindSources(args []string) []string {
	var srcs []string
	for i, a := range args {
		if spec, ok := mountSpec(a, args, i); ok {
			srcs = appendBindSrc(srcs, spec)
		}
	}
	return srcs
}

func mountSpec(arg string, args []string, i int) (string, bool) {
	if arg == "--mount" && i+1 < len(args) {
		return args[i+1], true
	}
	if rest, ok := strings.CutPrefix(arg, "--mount="); ok {
		return rest, true
	}
	return "", false
}

func appendBindSrc(dst []string, spec string) []string {
	if !strings.Contains(spec, "type=bind") {
		return dst
	}
	for part := range strings.SplitSeq(spec, ",") {
		if v, ok := strings.CutPrefix(part, "src="); ok {
			return append(dst, v)
		}
		if v, ok := strings.CutPrefix(part, "source="); ok {
			return append(dst, v)
		}
	}
	return dst
}

// logBindSources records host-side resolution of each bind source. A
// "host=exists" line followed by docker reporting the path missing is the
// fingerprint of a Docker Desktop file-share cache race.
func logBindSources(args []string) {
	for _, src := range extractBindSources(args) {
		_, err := os.Lstat(src)
		log.Infof("docker bind: src=%s exists=%t", src, err == nil)
	}
}

var hostEnvOnce sync.Once

func logHostEnvOnce(ctx context.Context, helper *docker.DockerHelper) {
	hostEnvOnce.Do(func() {
		log.Infof("docker host: %s", collectHostEnv(ctx, helper))
	})
}

func collectHostEnv(ctx context.Context, helper *docker.DockerHelper) string {
	parts := []string{fmt.Sprintf("os=%s/%s", runtime.GOOS, runtime.GOARCH)}
	if helper != nil {
		if v := dockerInfoField(ctx, helper, "{{.ServerVersion}}"); v != "" {
			parts = append(parts, "server="+v)
		}
		if v := dockerInfoField(ctx, helper, "{{.Driver}}"); v != "" {
			parts = append(parts, "storageDriver="+v)
		}
	}
	return strings.Join(parts, " ")
}

func dockerInfoField(ctx context.Context, helper *docker.DockerHelper, tmpl string) string {
	if ctx == nil {
		ctx = context.Background()
	}
	tctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := helper.Run(
		tctx,
		[]string{"info", "--format", tmpl},
		nil,
		&out,
		io.Discard,
	); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

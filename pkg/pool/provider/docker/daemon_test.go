package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/provider"
)

const (
	verbInspect = "inspect"
	flagAll     = "--all"
	flagForce   = "--force"
	spaced      = "a b"
	shellBait   = "$(id)"
)

var errNoSuchObject = errors.New("no such object")

type object struct {
	kind   string
	id     string
	name   string
	labels map[string]string
}

// daemon is an in-memory Docker that rejects any command or flag the
// provider is not meant to use, so a wrong argument fails a test.
type daemon struct {
	mu     sync.Mutex
	objs   []object
	calls  [][]string
	seq    int
	loose  bool
	before func(args []string)
	rmErr  error
}

func (d *daemon) Run(ctx context.Context, args ...string) ([]byte, error) {
	d.mu.Lock()
	d.calls = append(d.calls, slices.Clone(args))
	before := d.before
	d.mu.Unlock()
	if before != nil {
		before(args)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	kind, ok := map[string]string{nounContainer: kindWorkspace, kindNetwork: kindNetwork}[args[0]]
	if !ok || len(args) < 2 {
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	switch args[1] {
	case "ls":
		return d.ls(kind, args[2:])
	case verbInspect:
		return d.inspect(kind, args[2:])
	case "rm":
		return d.rm(kind, args[2:])
	}
	return nil, fmt.Errorf("unexpected verb %v", args)
}

func (d *daemon) ls(kind string, args []string) ([]byte, error) {
	filters, err := lsFilters(kind, args)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	for _, o := range d.objs {
		if o.kind == kind && (d.loose || o.matches(filters)) {
			out.WriteString(o.id + "\n")
		}
	}
	return []byte(out.String()), nil
}

func lsFilters(kind string, args []string) (map[string]string, error) {
	filters := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--filter" && i+1 < len(args) {
			i++
			addFilter(filters, args[i])
			continue
		}
		if !lsFlag(kind, args[i]) {
			return nil, fmt.Errorf("unexpected ls argument %q", args[i])
		}
		seen[args[i]] = true
	}
	if !seen["--quiet"] || !seen["--no-trunc"] {
		return nil, fmt.Errorf("ls needs --quiet and --no-trunc, got %v", args)
	}
	return filters, nil
}

func lsFlag(kind, arg string) bool {
	return arg == "--quiet" || arg == "--no-trunc" || (kind == kindWorkspace && arg == flagAll)
}

func addFilter(filters map[string]string, filter string) {
	key, value, _ := strings.Cut(filter, "=")
	if key == "label" {
		labelKey, labelValue, _ := strings.Cut(value, "=")
		key, value = "label:"+labelKey, labelValue
	}
	filters[key] = value
}

func (o object) matches(filters map[string]string) bool {
	for k, v := range filters {
		if k == "id" && o.id != v {
			return false
		}
		if lk, ok := strings.CutPrefix(k, "label:"); ok && o.labels[lk] != v {
			return false
		}
	}
	return true
}

func (d *daemon) inspect(kind string, args []string) ([]byte, error) {
	if len(args) < 4 || args[0] != "--format" || args[1] != "{{json .}}" || args[2] != "--" {
		return nil, fmt.Errorf("unexpected inspect arguments %v", args)
	}
	var out strings.Builder
	for _, id := range args[3:] {
		o, ok := d.find(kind, id)
		if !ok {
			return nil, errNoSuchObject
		}
		doc := map[string]any{"Id": o.id, "Name": o.name, "Labels": o.labels}
		if kind == kindWorkspace {
			doc = map[string]any{
				"Id": o.id, "Name": "/" + o.name, "Config": map[string]any{"Labels": o.labels},
			}
		}
		line, _ := json.Marshal(doc)
		out.Write(line)
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

func (d *daemon) rm(kind string, args []string) ([]byte, error) {
	id, err := rmOperand(kind, args)
	if err != nil {
		return nil, err
	}
	if d.rmErr != nil {
		return nil, d.rmErr
	}
	for i, o := range d.objs {
		if o.kind == kind && o.id == id {
			d.objs = slices.Delete(d.objs, i, i+1)
			return nil, nil
		}
	}
	return nil, errNoSuchObject
}

func rmOperand(kind string, args []string) (string, error) {
	flags := []string{flagForce, "--volumes"}
	if kind == kindNetwork {
		flags = nil
	}
	n := len(args)
	if n < 2 || args[n-2] != "--" || !slices.Equal(args[:n-2], flags) {
		return "", fmt.Errorf("unexpected rm arguments %v", args)
	}
	return args[n-1], nil
}

func (d *daemon) find(kind, id string) (object, bool) {
	for _, o := range d.objs {
		if o.kind == kind && o.id == id {
			return o, true
		}
	}
	return object{}, false
}

func labelsFor(pool, session string) map[string]string {
	labels := map[string]string{provider.LabelPool: pool}
	if session != "" {
		labels[provider.LabelSession] = session
	}
	return labels
}

func (d *daemon) add(kind, name string, labels map[string]string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%d", name, d.seq))
	id := hex.EncodeToString(sum[:])
	d.objs = append(d.objs, object{kind: kind, id: id, name: name, labels: maps.Clone(labels)})
	return id
}

func (d *daemon) replace(id string) string {
	d.mu.Lock()
	var old object
	for i, o := range d.objs {
		if o.id == id {
			old = o
			d.objs = slices.Delete(d.objs, i, i+1)
			break
		}
	}
	d.mu.Unlock()
	return d.add(old.kind, old.name, old.labels)
}

func (d *daemon) relabel(id, key, value string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.objs {
		if o.id == id {
			o.labels[key] = value
		}
	}
}

func (d *daemon) has(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.ContainsFunc(d.objs, func(o object) bool { return o.id == id })
}

func (d *daemon) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.objs)
}

func (d *daemon) commands() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.calls)
}

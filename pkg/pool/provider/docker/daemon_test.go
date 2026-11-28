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
	"strconv"
	"strings"
	"sync"

	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/provider/providertest"
)

const (
	verbInspect     = "inspect"
	simulatedSubnet = "172.30.0.0/16"
	flagAll         = "--all"
	flagForce       = "--force"
	flagNoTrunc     = "--no-trunc"
	flagQuiet       = "--quiet"
	flagFormat      = "--format"
	flagVolumes     = "--volumes"
	spaced          = "a b"
	shellBait       = "$(id)"
)

var (
	errNoSuchObject = errors.New("no such object")
	errInjected     = errors.New("injected failure")
)

type object struct {
	kind     string
	id       string
	name     string
	labels   map[string]string
	internal bool
	icc      string
	network  string
	image    string
	starts   int
	began    int
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
	once   func()
	rmErr  error
	// failAt makes the call with that 1-based number return errInjected. With
	// failApplied the command has already taken effect, as when a CLI is killed
	// after the daemon did the work.
	failAt      int
	failApplied bool
	// lateAfter makes a create that arrives with a finished context still
	// happen in the daemon, just before that many later calls, as when a
	// killed CLI leaves its work running.
	lateAfter int
	late      *lateCreate
	// createOut replaces what a network create prints.
	createOut string
	beganSeq  int
	// absent lists images the daemon does not hold. A pull adds them, unless
	// pullErr is set.
	absent  map[string]bool
	pullErr error
}

type lateCreate struct {
	args  []string
	after int
}

func (d *daemon) Run(ctx context.Context, args ...string) ([]byte, error) {
	n, before, once := d.enter(args)
	if before != nil {
		before(args)
	}
	if once != nil {
		once()
	}
	if err := ctx.Err(); err != nil {
		d.delayCreate(args)
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.apply(n, args)
}

func (d *daemon) enter(args []string) (int, func([]string), func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, slices.Clone(args))
	d.landLate()
	before, once := d.before, d.once
	d.once = nil
	return len(d.calls), before, once
}

func (d *daemon) apply(n int, args []string) ([]byte, error) {
	if n == d.failAt && !d.failApplied {
		return nil, errInjected
	}
	out, err := d.dispatch(args)
	if n == d.failAt && d.failApplied {
		return nil, errInjected
	}
	return d.printed(out, args), err
}

func (d *daemon) printed(out []byte, args []string) []byte {
	if d.createOut != "" && args[0] == kindNetwork && args[1] == verbCreate && out != nil {
		return []byte(d.createOut)
	}
	return out
}

// landLate runs with d.mu held.
func (d *daemon) landLate() {
	if d.late == nil {
		return
	}
	if d.late.after--; d.late.after == 0 {
		args := d.late.args
		d.late = nil
		_, _ = d.dispatch(args)
	}
}

// flushLate lands a create that no later call reached, as when the daemon
// finishes after the provider stopped looking.
func (d *daemon) flushLate() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.late != nil {
		d.late.after = 1
		d.landLate()
	}
}

func (d *daemon) delayCreate(args []string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lateAfter > 0 && d.late == nil && len(args) > 1 && args[1] == verbCreate {
		d.late = &lateCreate{args: slices.Clone(args), after: d.lateAfter}
	}
}

func (d *daemon) dispatch(args []string) ([]byte, error) {
	if out, err, ok := d.imageCommand(args); ok {
		return out, err
	}
	kind, ok := map[string]string{nounContainer: kindWorkspace, kindNetwork: kindNetwork}[args[0]]
	if !ok || len(args) < 2 {
		return nil, fmt.Errorf("unexpected command %v", args)
	}
	verbs := map[string]func(string, []string) ([]byte, error){
		"ls":        d.ls,
		verbInspect: d.inspect,
		"rm":        d.rm,
		verbCreate:  d.create,
		verbStart:   d.start,
	}
	handle, ok := verbs[args[1]]
	if !ok {
		return nil, fmt.Errorf("unexpected verb %v", args)
	}
	return handle(kind, args[2:])
}

func (d *daemon) imageCommand(args []string) ([]byte, error, bool) {
	if len(args) != 4 || args[2] != "--" {
		return nil, nil, false
	}
	switch {
	case args[0] == nounImage && args[1] == verbInspect:
		out, err := d.imageInspect(args[3])
		return out, err, true
	case args[0] == verbPull && args[1] == flagQuiet:
		return nil, d.pull(args[3]), true
	}
	return nil, nil, false
}

func (d *daemon) imageInspect(ref string) ([]byte, error) {
	if d.absent[ref] {
		return nil, errNoSuchObject
	}
	return []byte("[]\n"), nil
}

func (d *daemon) pull(ref string) error {
	if d.pullErr != nil {
		return d.pullErr
	}
	delete(d.absent, ref)
	return nil
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
	if !seen[flagQuiet] || !seen[flagNoTrunc] {
		return nil, fmt.Errorf("ls needs --quiet and --no-trunc, got %v", args)
	}
	return filters, nil
}

func lsFlag(kind, arg string) bool {
	return arg == flagQuiet || arg == flagNoTrunc || (kind == kindWorkspace && arg == flagAll)
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
		if k == "name" && o.name != strings.Trim(v, "^$") {
			return false
		}
		if lk, ok := strings.CutPrefix(k, "label:"); ok && o.labels[lk] != v {
			return false
		}
	}
	return true
}

func (d *daemon) inspect(kind string, args []string) ([]byte, error) {
	if len(args) < 4 || args[0] != flagFormat || args[1] != "{{json .}}" || args[2] != "--" {
		return nil, fmt.Errorf("unexpected inspect arguments %v", args)
	}
	var out strings.Builder
	for _, id := range args[3:] {
		o, ok := d.find(kind, id)
		if !ok {
			return nil, errNoSuchObject
		}
		line, _ := json.Marshal(o.inspectDoc())
		out.Write(line)
		out.WriteString("\n")
	}
	return []byte(out.String()), nil
}

func (o object) inspectDoc() any {
	if o.kind == kindWorkspace {
		return inspected{ID: o.id, Name: "/" + o.name, Config: inspectedConfig{Labels: o.labels}}
	}
	return netInfo{
		ID: o.id, Name: o.name, Labels: o.labels, Driver: driverNet, Internal: o.internal,
		Options: map[string]string{optICC: o.icc},
		IPAM:    ipam{Config: []ipamConfig{{Subnet: simulatedSubnet, Gateway: "172.30.0.1"}}},
	}
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

// start counts starts, because Docker accepts a start of a running container.
// A started workspace keeps running, and a repeat start leaves it alone.
func (d *daemon) start(kind string, args []string) ([]byte, error) {
	if kind != kindWorkspace || len(args) != 2 || args[0] != "--" {
		return nil, fmt.Errorf("unexpected start arguments %v", args)
	}
	for i, o := range d.objs {
		if o.kind == kind && o.id == args[1] {
			d.objs[i].starts++
			if o.began == 0 {
				d.beganSeq++
				d.objs[i].began = d.beganSeq
			}
			return []byte(o.name + "\n"), nil
		}
	}
	return nil, errNoSuchObject
}

func (d *daemon) snapshot() []object {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.objs)
}

func rmOperand(kind string, args []string) (string, error) {
	flags := []string{flagForce, flagVolumes}
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
	labels = maps.Clone(labels)
	if kind == kindSidecar {
		kind = kindWorkspace
		if labels == nil {
			labels = map[string]string{}
		}
		labels[provider.LabelRole] = provider.RoleSidecar
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seq++
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%d", name, d.seq))
	id := hex.EncodeToString(sum[:])
	d.objs = append(d.objs, object{kind: kind, id: id, name: name, labels: labels})
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

func (d *daemon) seed(kind, name string, labels map[string]string) {
	d.mu.Lock()
	d.objs = slices.DeleteFunc(
		d.objs,
		func(o object) bool { return o.resourceKind() == kind && o.name == name },
	)
	d.mu.Unlock()
	d.add(kind, name, labels)
}

func (d *daemon) inventory() []provider.Resource {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]provider.Resource, 0, len(d.objs))
	for _, o := range d.objs {
		out = append(out, provider.Resource{
			Kind: o.resourceKind(), Name: o.name, UID: o.id, Labels: maps.Clone(o.labels),
		})
	}
	return out
}

func (o object) resourceKind() string {
	if o.kind == kindWorkspace && o.labels[provider.LabelRole] == provider.RoleSidecar {
		return kindSidecar
	}
	return o.kind
}

func (d *daemon) workspaceState(pool, id string) providertest.WorkspaceState {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.objs {
		if o.resourceKind() == kindWorkspace && o.labels[provider.LabelPool] == pool &&
			o.labels[provider.LabelSession] == id {
			return providertest.WorkspaceState{Running: o.began != 0, Began: strconv.Itoa(o.began)}
		}
	}
	return providertest.WorkspaceState{}
}

func (d *daemon) named(kind, name string) (object, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, o := range d.objs {
		if o.kind == kind && o.name == name {
			return o, true
		}
	}
	return object{}, false
}

type createArgs struct {
	labels   map[string]string
	name     string
	network  string
	operand  string
	internal bool
	icc      string
	noPull   bool
}

func (d *daemon) create(kind string, args []string) ([]byte, error) {
	c, err := parseCreate(kind, args)
	if err != nil {
		return nil, err
	}
	o := object{kind: kind, name: c.name, labels: c.labels, internal: c.internal, icc: c.icc}
	if kind == kindWorkspace {
		if _, ok := d.find(kindNetwork, c.network); !ok {
			return nil, errNoSuchObject
		}
		o.network, o.image = c.network, c.operand
	}
	if slices.ContainsFunc(
		d.objs,
		func(x object) bool { return x.kind == kind && x.name == c.name },
	) {
		return nil, &CommandError{
			Stderr: fmt.Sprintf("name %q is already in use", c.name),
			Err:    errInjected,
		}
	}
	d.seq++
	sum := sha256.Sum256(fmt.Appendf(nil, "%s/%d", c.name, d.seq))
	o.id = hex.EncodeToString(sum[:])
	d.objs = append(d.objs, o)
	return []byte(o.id + "\n"), nil
}

func parseCreate(kind string, args []string) (createArgs, error) {
	c := createArgs{labels: map[string]string{}}
	i := 0
	for ; i < len(args) && args[i] != "--"; i++ {
		var err error
		if i, err = c.flag(kind, args, i); err != nil {
			return c, err
		}
	}
	if i != len(args)-2 {
		return c, fmt.Errorf("create needs one operand after --, got %v", args)
	}
	c.operand = args[len(args)-1]
	return c, c.validate(kind, args)
}

func (c *createArgs) validate(kind string, args []string) error {
	if kind == kindNetwork {
		c.name = c.operand
		return c.validateNetwork(args)
	}
	if c.name == "" || c.network == "" {
		return fmt.Errorf("container create needs --name and --network: %v", args)
	}
	if !c.noPull {
		return fmt.Errorf("container create must not pull, got %v", args)
	}
	return nil
}

func (c *createArgs) validateNetwork(args []string) error {
	if _, shared := c.labels[provider.LabelShared]; shared {
		if c.internal || c.icc != iccDisabled || len(c.labels) != 1 {
			return fmt.Errorf("unexpected shared bridge arguments: %v", args)
		}
		return nil
	}
	if !c.internal || c.icc != "" {
		return fmt.Errorf("a session network must be internal: %v", args)
	}
	return nil
}

func (c *createArgs) flag(kind string, args []string, i int) (int, error) {
	if args[i] == "--internal" && kind == kindNetwork {
		c.internal = true
		return i, nil
	}
	if args[i] == flagPullNever && kind == kindWorkspace {
		c.noPull = true
		return i, nil
	}
	set := c.setters(kind)[args[i]]
	if set == nil || i+1 >= len(args) {
		return i, fmt.Errorf("unexpected create argument %q", args[i])
	}
	set(args[i+1])
	return i + 1, nil
}

func (c *createArgs) setters(kind string) map[string]func(string) {
	setters := map[string]func(string){
		"--label": func(v string) {
			key, value, _ := strings.Cut(v, "=")
			c.labels[key] = value
		},
	}
	if kind == kindWorkspace {
		setters["--name"] = func(v string) { c.name = v }
		setters["--network"] = func(v string) { c.network = v }
	} else {
		setters["--opt"] = func(v string) {
			if option, ok := strings.CutPrefix(v, optICC+"="); ok {
				c.icc = option
			}
		}
	}
	return setters
}

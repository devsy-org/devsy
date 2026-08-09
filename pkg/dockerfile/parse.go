package dockerfile

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/pkg/scanner"
	"github.com/moby/buildkit/frontend/dockerfile/command"
	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

var syntaxDirectiveRegex = regexp.MustCompile(`(?m)^[\s\t]*#[\s\t]*syntax=.*$`)

func (d *Dockerfile) FindUserStatement(
	buildArgs, baseImageEnv map[string]string,
	target string,
) string {
	stage, ok := d.StagesByTarget[target]
	if !ok && len(d.Stages) > 0 {
		stage = d.Stages[len(d.Stages)-1]
	}

	seenStages := make(map[string]bool, 4)
	for stage != nil {
		stageKey := getStageIdentifier(&stage.BaseStage)
		if seenStages[stageKey] {
			return ""
		}
		seenStages[stageKey] = true

		if len(stage.Users) > 0 {
			return d.expandVariables(
				stage.Users[len(stage.Users)-1].Key,
				buildArgs,
				baseImageEnv,
				&stage.BaseStage,
			)
		}

		if stage.Image == "" {
			return ""
		}

		image := d.expandVariables(
			stage.Image,
			buildArgs,
			baseImageEnv,
			&d.Preamble.BaseStage,
		)
		stage, ok = d.StagesByTarget[image]
		if !ok {
			return ""
		}
	}
	return ""
}

func getStageIdentifier(stage *BaseStage) string {
	return stage.Image + "-" + stage.Target
}

func (d *Dockerfile) FindBaseImage(buildArgs map[string]string, target string) string {
	return d.findBaseImage(buildArgs, target, make(map[string]bool, len(d.Stages)))
}

func (d *Dockerfile) BuildContextFiles() []string {
	files := make([]string, 0, 8)
	for _, stage := range d.Stages {
		for _, in := range stage.Instructions {
			if cmd, err := instructions.ParseCommand(in); err == nil {
				if addCmd, ok := cmd.(*instructions.AddCommand); ok {
					files = append(files, addCmd.SourcePaths...)
				} else if copyCmd, ok := cmd.(*instructions.CopyCommand); ok {
					files = append(files, copyCmd.SourcePaths...)
				}
			}
		}
	}
	return files
}

var defaultShellLexer = shell.NewLex('\\')

type environmentResolver struct {
	dockerfile   *Dockerfile
	buildArgs    map[string]string
	baseImageEnv map[string]string
	stage        *BaseStage
	_            int // unused untilLine field for compatibility
}

func (e *environmentResolver) Get(key string) (string, bool) {
	val, ok := e.dockerfile.resolveVariable(e.buildArgs, e.baseImageEnv, key, e.stage)
	return val, ok
}

func (e *environmentResolver) Keys() []string {
	keys := make([]string, 0, len(e.buildArgs)+len(e.baseImageEnv))
	for k := range e.buildArgs {
		keys = append(keys, k)
	}
	for k := range e.baseImageEnv {
		keys = append(keys, k)
	}
	return keys
}

func RemoveSyntaxVersion(dockerfileContent string) string {
	return syntaxDirectiveRegex.ReplaceAllString(dockerfileContent, "")
}

func EnsureFinalStageName(dockerfileContent, defaultLastStageName string) (string, string, error) {
	result, err := parser.Parse(strings.NewReader(dockerfileContent))
	if err != nil {
		return "", "", err
	}

	lastChild := lastFromNode(result.AST.Children)
	if lastChild == nil {
		return "", "", fmt.Errorf("no FROM statement in dockerfile")
	}
	if lastChild.Next == nil {
		return "", "", fmt.Errorf("cannot parse FROM statement in dockerfile")
	}

	if hasStageAlias(lastChild) {
		return lastChild.Next.Next.Next.Value, "", nil
	}

	lastChild.Next.Next = &parser.Node{
		Value: "AS",
		Next:  &parser.Node{Value: defaultLastStageName},
	}
	return defaultLastStageName, ReplaceInDockerfile(dockerfileContent, lastChild), nil
}

func lastFromNode(children []*parser.Node) *parser.Node {
	var lastChild *parser.Node
	for _, child := range children {
		if strings.ToLower(child.Value) == command.From {
			lastChild = child
		}
	}
	return lastChild
}

func hasStageAlias(node *parser.Node) bool {
	return node.Next.Next != nil && node.Next.Next.Next != nil &&
		strings.EqualFold(node.Next.Next.Value, "as")
}

func ReplaceInDockerfile(dockerfileContent string, node *parser.Node) string {
	scan := scanner.NewScanner(strings.NewReader(dockerfileContent))
	var lines []string
	for lineNumber := 1; scan.Scan(); lineNumber++ {
		if lineNumber >= node.StartLine && lineNumber <= node.EndLine {
			lines = append(lines, FormatNode(node))
		} else {
			lines = append(lines, scan.Text())
		}
	}
	return strings.Join(lines, "\n")
}

type Dockerfile struct {
	Raw string

	Directives []*parser.Directive
	Preamble   *Preamble
	Syntax     string // https://docs.docker.com/build/concepts/dockerfile/#dockerfile-syntax

	Stages         []*Stage
	StagesByTarget map[string]*Stage
}

type Preamble struct {
	BaseStage
}

type Stage struct {
	BaseStage
	Users []instructions.KeyValuePair
}

type BaseStage struct {
	Image  string
	Target string

	Envs         []instructions.KeyValuePair
	Args         []instructions.KeyValuePairOptional
	Instructions []*parser.Node
}

func (d *Dockerfile) Dump() string {
	result := make([]string, 0, len(d.Stages))
	for _, stage := range d.Stages {
		if dump := FormatNodes(stage.Instructions); dump != "" {
			result = append(result, dump)
		}
	}
	return strings.Join(result, "\n")
}

// findBaseImage recurses through stage references, tracking visited targets in
// seen to break circular references (e.g. `FROM stage2 AS stage1` followed by
// `FROM stage1 AS stage2`) that would otherwise overflow the stack.
func (d *Dockerfile) findBaseImage(
	buildArgs map[string]string,
	target string,
	seen map[string]bool,
) string {
	stage := d.StagesByTarget[target]
	if stage == nil && len(d.Stages) > 0 {
		stage = d.Stages[len(d.Stages)-1]
	}
	if stage == nil {
		return ""
	}

	if seen[target] {
		return ""
	}
	seen[target] = true

	image := d.expandVariables(stage.Image, buildArgs, nil, &d.Preamble.BaseStage)

	if _, ok := d.StagesByTarget[image]; ok {
		return d.findBaseImage(buildArgs, image, seen)
	}

	return image
}

func (d *Dockerfile) expandVariables(
	val string,
	buildArgs, baseImageEnv map[string]string,
	stage *BaseStage,
) string {
	result, _, err := defaultShellLexer.ProcessWord(
		val,
		&environmentResolver{d, buildArgs, baseImageEnv, stage, 0},
	)
	if err != nil {
		return val
	}
	return result
}

func (d *Dockerfile) resolveVariable(
	buildArgs, baseImageEnv map[string]string,
	variable string,
	stage *BaseStage,
) (string, bool) {
	if buildArgs == nil {
		buildArgs = make(map[string]string)
	}

	startedFromPreamble := (stage == &d.Preamble.BaseStage)
	seenStages := make(map[string]bool, 4)

	for {
		if seenStages[getStageIdentifier(stage)] {
			return "", false
		}
		seenStages[getStageIdentifier(stage)] = true

		if value, found := d.resolveFromArgs(buildArgs, variable, stage); found {
			return value, true
		}

		if value, found := d.resolveFromEnvs(buildArgs, baseImageEnv, variable, stage); found {
			return value, true
		}

		if stage == &d.Preamble.BaseStage {
			return d.resolveFromBaseImageEnv(baseImageEnv, variable)
		}

		if !startedFromPreamble {
			return d.resolveFromBaseImageEnv(baseImageEnv, variable)
		}

		stage = d.getParentStage(buildArgs, baseImageEnv, stage)
	}
}

func (d *Dockerfile) resolveFromArgs(
	buildArgs map[string]string,
	variable string,
	stage *BaseStage,
) (string, bool) {
	for _, v := range slices.Backward(stage.Args) {
		arg := &v
		if arg.Key != variable {
			continue
		}
		if val := buildArgs[arg.Key]; val != "" {
			return strings.Trim(val, "\"'"), true
		}
		if arg.Value != nil && *arg.Value != "" {
			return strings.Trim(*arg.Value, "\"'"), true
		}
		return "", true
	}
	return "", false
}

func (d *Dockerfile) resolveFromEnvs(
	buildArgs, baseImageEnv map[string]string,
	variable string,
	stage *BaseStage,
) (string, bool) {
	for _, v := range slices.Backward(stage.Envs) {
		env := &v
		if env.Key != variable {
			continue
		}
		if env.Value != "" {
			return d.expandVariables(env.Value, buildArgs, baseImageEnv, stage), true
		}
		return "", true
	}
	return "", false
}

func (d *Dockerfile) resolveFromBaseImageEnv(
	baseImageEnv map[string]string,
	variable string,
) (string, bool) {
	if baseImageEnv != nil {
		if value, exists := baseImageEnv[variable]; exists {
			return value, true
		}
	}
	return "", false
}

func (d *Dockerfile) getParentStage(
	buildArgs, baseImageEnv map[string]string,
	stage *BaseStage,
) *BaseStage {
	image := d.expandVariables(
		stage.Image,
		buildArgs,
		baseImageEnv,
		&d.Preamble.BaseStage,
	)
	if foundStage, ok := d.StagesByTarget[image]; ok {
		return &foundStage.BaseStage
	}
	return &d.Preamble.BaseStage
}

func Parse(dockerfileContent string) (*Dockerfile, error) {
	result, err := parser.Parse(strings.NewReader(dockerfileContent))
	if err != nil {
		return nil, err
	}
	if len(result.AST.Children) == 0 {
		return nil, fmt.Errorf("received empty Dockerfile")
	}

	d := &Dockerfile{
		Raw:            dockerfileContent,
		Preamble:       &Preamble{},
		StagesByTarget: make(map[string]*Stage),
	}

	d.parseDirectives(dockerfileContent)

	// Parse instructions with single loop
	isPreamble := true
	for _, instruction := range result.AST.Children {
		isPreamble = d.appendInstruction(instruction, isPreamble)
	}

	d.indexStagesByTarget()

	return d, nil
}

func (d *Dockerfile) parseDirectives(dockerfileContent string) {
	directiveParser := parser.DirectiveParser{}
	directives, _ := directiveParser.ParseAll([]byte(dockerfileContent))
	if len(directives) == 0 {
		return
	}

	d.Directives = directives
	for _, directive := range directives {
		if strings.EqualFold(directive.Name, "syntax") {
			d.Syntax = directive.Value
			break
		}
	}
}

func (d *Dockerfile) appendInstruction(instruction *parser.Node, isPreamble bool) bool {
	cmd := strings.ToLower(instruction.Value)

	if isPreamble && cmd == command.From {
		d.Stages = append(d.Stages, parseStage(instruction))
		return false
	}

	if isPreamble {
		d.appendPreambleInstruction(instruction, cmd)
		return true
	}

	if cmd == command.From {
		d.Stages = append(d.Stages, parseStage(instruction))
		return false
	}

	d.appendStageInstruction(instruction, cmd)
	return false
}

func (d *Dockerfile) appendPreambleInstruction(instruction *parser.Node, cmd string) {
	d.Preamble.Instructions = append(d.Preamble.Instructions, instruction)
	switch cmd {
	case command.Env:
		d.Preamble.Envs = append(d.Preamble.Envs, parseEnv(instruction)...)
	case command.Arg:
		d.Preamble.Args = append(d.Preamble.Args, parseArg(instruction))
	}
}

func (d *Dockerfile) appendStageInstruction(instruction *parser.Node, cmd string) {
	lastStage := d.Stages[len(d.Stages)-1]
	lastStage.Instructions = append(lastStage.Instructions, instruction)
	switch cmd {
	case command.Env:
		lastStage.Envs = append(lastStage.Envs, parseEnv(instruction)...)
	case command.Arg:
		lastStage.Args = append(lastStage.Args, parseArg(instruction))
	case command.User:
		lastStage.Users = append(lastStage.Users, parseUser(instruction))
	}
}

func (d *Dockerfile) indexStagesByTarget() {
	for _, stage := range d.Stages {
		if stage.Target != "" {
			d.StagesByTarget[stage.Target] = stage
		}
	}
}

func parseUser(instruction *parser.Node) instructions.KeyValuePair {
	value := instruction.Next.Value
	if strings.Contains(value, ":") && !strings.HasPrefix(value, "${") {
		value = strings.Split(value, ":")[0]
	}
	return instructions.KeyValuePair{Key: value}
}

func parseArg(instruction *parser.Node) instructions.KeyValuePairOptional {
	node := instruction.Next
	if node.Next != nil {
		value := node.Next.Value
		return instructions.KeyValuePairOptional{Key: node.Value, Value: &value}
	}
	if strings.Contains(node.Value, "=") {
		parts := strings.SplitN(node.Value, "=", 2)
		return instructions.KeyValuePairOptional{Key: parts[0], Value: &parts[1]}
	}
	return instructions.KeyValuePairOptional{Key: node.Value}
}

func parseEnv(instruction *parser.Node) []instructions.KeyValuePair {
	envs := make([]instructions.KeyValuePair, 0, 2) // Most ENV instructions have 1-2 pairs
	for node := instruction.Next; node != nil && node.Next != nil; node = node.Next.Next {
		envs = append(envs, instructions.KeyValuePair{
			Key:   strings.TrimSpace(node.Value),
			Value: strings.Trim(strings.ReplaceAll(node.Next.Value, "\\", ""), "\"'"),
		})
	}
	return envs
}

func parseStage(instruction *parser.Node) *Stage {
	var image, target string
	if next := instruction.Next; next != nil {
		image = next.Value
		if next.Next != nil && strings.EqualFold(next.Next.Value, "as") &&
			next.Next.Next != nil && next.Next.Next.Value != "" {
			target = next.Next.Next.Value
		}
	}
	return &Stage{
		BaseStage: BaseStage{
			Image:        image,
			Target:       target,
			Instructions: []*parser.Node{instruction},
		},
	}
}

func FormatNodes(nodes []*parser.Node) string {
	if len(nodes) == 0 {
		return ""
	}
	children := make([]string, len(nodes))
	for i, n := range nodes {
		children[i] = FormatNode(n)
	}
	return strings.Join(children, "\n")
}

func FormatNode(node *parser.Node) string {
	var out strings.Builder
	if len(node.PrevComment) > 0 {
		out.WriteString("# ")
		out.WriteString(strings.Join(node.PrevComment, "\n# "))
		if node.Value != "" {
			out.WriteByte('\n')
		}
	}

	if node.Value != "" {
		out.WriteString(node.Value)
	}
	for _, child := range node.Flags {
		out.WriteByte(' ')
		out.WriteString(child)
	}
	if node.Next != nil {
		out.WriteByte(' ')
		out.WriteString(FormatNode(node.Next))
	}

	return out.String()
}

package runargs

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/devsy-org/devsy/pkg/config"
)

type optionKind int

const (
	unknownOption optionKind = iota
	switchOption
	valueOption
	labelOption
	labelFileOption
	imageSourceOption
)

type Grammar struct {
	options             map[string]optionKind
	attachedShortValues bool
	dashValues          bool
}

type assignment struct {
	name  string
	kind  optionKind
	value string
}

func Validate(grammar Grammar, args []string, dir string) error {
	if err := validate(grammar, args, dir); err != nil {
		return fmt.Errorf("runArgs: %w", err)
	}
	return nil
}

func validate(grammar Grammar, args []string, dir string) error {
	assignments, err := grammar.parse(args)
	if err != nil {
		return err
	}
	var labels []string
	for _, a := range assignments {
		switch a.kind {
		case labelOption:
			labels = append(labels, a.value)
		case labelFileOption:
			fileLabels, err := readLabelFile(a.value, dir)
			if err != nil {
				return err
			}
			labels = append(labels, fileLabels...)
		}
	}
	return config.ValidateUserLabels(labels)
}

func (g Grammar) parse(args []string) ([]assignment, error) {
	var assignments []assignment
	for i := 0; i < len(args); i++ {
		a, consumesNext, err := g.parseArg(args[i])
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i+1, err)
		}
		if consumesNext {
			if i+1 == len(args) || !g.dashValues && strings.HasPrefix(args[i+1], "-") {
				return nil, fmt.Errorf("item %d: %s needs a value", i+1, a.name)
			}
			i++
			a.value = args[i]
		}
		assignments = append(assignments, a)
	}
	return assignments, nil
}

func (g Grammar) parseArg(arg string) (assignment, bool, error) {
	if arg == "-" || arg == "--" || !strings.HasPrefix(arg, "-") {
		return assignment{}, false, errors.New(
			"not an option or option value; the runtime would read it as the image",
		)
	}
	if strings.HasPrefix(arg, "--") {
		return g.parseLong(arg)
	}
	return g.parseShort(arg[1:])
}

func (g Grammar) parseLong(arg string) (assignment, bool, error) {
	name, value, hasValue := strings.Cut(arg, "=")
	a := assignment{name: name, kind: g.options[name], value: value}
	switch {
	case a.kind == imageSourceOption:
		return assignment{}, false, fmt.Errorf(
			"option %s makes the runtime read the image as a root filesystem", name,
		)
	case hasValue:
		return a, false, nil
	case a.kind == unknownOption:
		return assignment{}, false, fmt.Errorf(
			"unknown option %s; write it as %s=VALUE", name, name,
		)
	default:
		return a, a.kind != switchOption, nil
	}
}

func (g Grammar) parseShort(cluster string) (assignment, bool, error) {
	for i := range len(cluster) {
		a := assignment{name: "-" + cluster[i:i+1]}
		a.kind = g.options[a.name]
		rest := cluster[i+1:]
		if a.kind == unknownOption {
			return assignment{}, false, fmt.Errorf("unknown option %s", a.name)
		}
		if a.kind != switchOption || hasInlineValue(rest) {
			return g.shortValue(a, rest)
		}
	}
	return assignment{kind: switchOption}, false, nil
}

func (g Grammar) shortValue(a assignment, rest string) (assignment, bool, error) {
	switch {
	case hasInlineValue(rest):
		a.value = rest[1:]
		return a, false, nil
	case rest == "":
		return a, true, nil
	case g.attachedShortValues:
		a.value = rest
		return a, false, nil
	default:
		return assignment{}, false, fmt.Errorf("put the value of %s in the next item", a.name)
	}
}

func hasInlineValue(rest string) bool {
	return len(rest) > 1 && rest[0] == '='
}

func readLabelFile(path, dir string) ([]string, error) {
	resolved, err := resolveLabelFile(path, dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("read label file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("label file %q is not a regular file", path)
	}
	file, err := os.Open(filepath.Clean(resolved))
	if err != nil {
		return nil, fmt.Errorf("read label file: %w", err)
	}
	defer func() { _ = file.Close() }()
	return scanLabelFile(file)
}

func scanLabelFile(file *os.File) ([]string, error) {
	var labels []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimLeftFunc(strings.TrimPrefix(scanner.Text(), "\uFEFF"), unicode.IsSpace)
		if !strings.HasPrefix(line, "#") && !strings.Contains(line, "=") &&
			strings.HasSuffix(line, "*") {
			return nil, fmt.Errorf(
				"label file entry %q copies variables from the runtime environment", line,
			)
		}
		labels = append(labels, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read label file: %w", err)
	}
	return labels, nil
}

func resolveLabelFile(path, dir string) (string, error) {
	if err := checkLabelFilePath(path); err != nil {
		return "", err
	}
	if filepath.IsAbs(path) {
		return path, nil
	}
	return filepath.Join(dir, path), nil
}

func checkLabelFilePath(path string) error {
	if strings.ContainsAny(path, ",\"\r\n") {
		return fmt.Errorf(
			"label file path %q must not contain a comma, quote or line break", path,
		)
	}
	if slices.Contains(strings.FieldsFunc(path, isPathSeparator), "..") {
		return fmt.Errorf("label file path %q must not contain a .. element", path)
	}
	if !filepath.IsAbs(path) &&
		(filepath.VolumeName(path) != "" || strings.IndexFunc(path, isPathSeparator) == 0) {
		return fmt.Errorf(
			"label file path %q must be absolute or relative to the workspace", path,
		)
	}
	return nil
}

func isPathSeparator(r rune) bool {
	return r == '/' || r == '\\'
}

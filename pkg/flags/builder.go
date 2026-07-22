package flags

import (
	"time"

	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
)

// Flag is a self-contained flag definition: its name, default, usage text, and
// the destination it binds to. Definitions are attached to a command with Add,
// which keeps registration uniform and lets shared flags be declared once.
type Flag interface {
	register(fs *flag.FlagSet)
}

// Add registers the given flag definitions on the command's local flags.
func Add(cmd *cobra.Command, defs ...Flag) {
	fs := cmd.Flags()
	for _, d := range defs {
		d.register(fs)
	}
}

// AddPersistent registers the given flag definitions on the command's
// persistent flags (inherited by subcommands).
func AddPersistent(cmd *cobra.Command, defs ...Flag) {
	fs := cmd.PersistentFlags()
	for _, d := range defs {
		d.register(fs)
	}
}

// options are the settings common to every flag kind, tweaked via With* chaining.
type options struct {
	shorthand string
	hidden    bool
}

func (o options) applyMeta(fs *flag.FlagSet, name string) {
	if o.hidden {
		_ = fs.MarkHidden(name)
	}
}

// StringFlag defines a string flag.
type StringFlag struct {
	dest  *string
	name  string
	def   string
	usage string
	options
}

// String binds a string flag to dest.
func String(dest *string, name, def, usage string) *StringFlag {
	return &StringFlag{dest: dest, name: name, def: def, usage: usage}
}

// Shorthand sets a single-letter shorthand (e.g. "L" for -L).
func (f *StringFlag) Shorthand(s string) *StringFlag { f.shorthand = s; return f }

// Hidden marks the flag hidden from help output.
func (f *StringFlag) Hidden() *StringFlag { f.hidden = true; return f }

func (f *StringFlag) register(fs *flag.FlagSet) {
	fs.StringVarP(f.dest, f.name, f.shorthand, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// BoolFlag defines a bool flag.
type BoolFlag struct {
	dest  *bool
	name  string
	def   bool
	usage string
	options
}

// Bool binds a bool flag to dest.
func Bool(dest *bool, name string, def bool, usage string) *BoolFlag {
	return &BoolFlag{dest: dest, name: name, def: def, usage: usage}
}

// Shorthand sets a single-letter shorthand.
func (f *BoolFlag) Shorthand(s string) *BoolFlag { f.shorthand = s; return f }

// Hidden marks the flag hidden from help output.
func (f *BoolFlag) Hidden() *BoolFlag { f.hidden = true; return f }

func (f *BoolFlag) register(fs *flag.FlagSet) {
	fs.BoolVarP(f.dest, f.name, f.shorthand, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// IntFlag defines an int flag.
type IntFlag struct {
	dest  *int
	name  string
	def   int
	usage string
	options
}

// Int binds an int flag to dest.
func Int(dest *int, name string, def int, usage string) *IntFlag {
	return &IntFlag{dest: dest, name: name, def: def, usage: usage}
}

// Hidden marks the flag hidden from help output.
func (f *IntFlag) Hidden() *IntFlag { f.hidden = true; return f }

func (f *IntFlag) register(fs *flag.FlagSet) {
	fs.IntVar(f.dest, f.name, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// Int64Flag defines an int64 flag.
type Int64Flag struct {
	dest  *int64
	name  string
	def   int64
	usage string
	options
}

// Int64 binds an int64 flag to dest.
func Int64(dest *int64, name string, def int64, usage string) *Int64Flag {
	return &Int64Flag{dest: dest, name: name, def: def, usage: usage}
}

// Hidden marks the flag hidden from help output.
func (f *Int64Flag) Hidden() *Int64Flag { f.hidden = true; return f }

func (f *Int64Flag) register(fs *flag.FlagSet) {
	fs.Int64Var(f.dest, f.name, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// StringSliceFlag defines a comma-separated / repeatable string slice flag.
type StringSliceFlag struct {
	dest  *[]string
	name  string
	def   []string
	usage string
	options
}

// StringSlice binds a comma-separated string slice flag to dest.
func StringSlice(dest *[]string, name string, def []string, usage string) *StringSliceFlag {
	return &StringSliceFlag{dest: dest, name: name, def: def, usage: usage}
}

// Shorthand sets a single-letter shorthand.
func (f *StringSliceFlag) Shorthand(s string) *StringSliceFlag { f.shorthand = s; return f }

// Hidden marks the flag hidden from help output.
func (f *StringSliceFlag) Hidden() *StringSliceFlag { f.hidden = true; return f }

func (f *StringSliceFlag) register(fs *flag.FlagSet) {
	fs.StringSliceVarP(f.dest, f.name, f.shorthand, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// StringArrayFlag defines a repeatable (not comma-split) string array flag.
type StringArrayFlag struct {
	dest  *[]string
	name  string
	def   []string
	usage string
	options
}

// StringArray binds a repeatable string array flag to dest (values are not
// comma-split, unlike StringSlice).
func StringArray(dest *[]string, name string, def []string, usage string) *StringArrayFlag {
	return &StringArrayFlag{dest: dest, name: name, def: def, usage: usage}
}

// Shorthand sets a single-letter shorthand.
func (f *StringArrayFlag) Shorthand(s string) *StringArrayFlag { f.shorthand = s; return f }

// Hidden marks the flag hidden from help output.
func (f *StringArrayFlag) Hidden() *StringArrayFlag { f.hidden = true; return f }

func (f *StringArrayFlag) register(fs *flag.FlagSet) {
	fs.StringArrayVarP(f.dest, f.name, f.shorthand, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

// ValueFlag defines a flag backed by a custom pflag.Value implementation.
type ValueFlag struct {
	value flag.Value
	name  string
	usage string
	options
}

// Value binds a custom pflag.Value flag.
func Value(value flag.Value, name, usage string) *ValueFlag {
	return &ValueFlag{value: value, name: name, usage: usage}
}

// Hidden marks the flag hidden from help output.
func (f *ValueFlag) Hidden() *ValueFlag { f.hidden = true; return f }

func (f *ValueFlag) register(fs *flag.FlagSet) {
	fs.Var(f.value, f.name, f.usage)
	f.applyMeta(fs, f.name)
}

// DurationFlag defines a time.Duration flag.
type DurationFlag struct {
	dest  *time.Duration
	name  string
	def   time.Duration
	usage string
	options
}

// Duration binds a time.Duration flag to dest.
func Duration(dest *time.Duration, name string, def time.Duration, usage string) *DurationFlag {
	return &DurationFlag{dest: dest, name: name, def: def, usage: usage}
}

// Hidden marks the flag hidden from help output.
func (f *DurationFlag) Hidden() *DurationFlag { f.hidden = true; return f }

func (f *DurationFlag) register(fs *flag.FlagSet) {
	fs.DurationVar(f.dest, f.name, f.def, f.usage)
	f.applyMeta(fs, f.name)
}

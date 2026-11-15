// Ported from packages/coding-agent/src/cli/experimental/command.ts (pi v1.0.0).

package experimentalcli

import (
	"fmt"
	"strings"
)

// NamedCommandInvocation is implemented by every parsed CLI command invocation.
type NamedCommandInvocation interface {
	CommandName() string
}

// CommandParseResult holds the outcome of parsing CLI arguments.
type CommandParseResult[TInvocation any] struct {
	Ok      bool
	Command TInvocation
	Errors  []string
}

// CommandExecutionResult holds the outcome of executing a parsed command.
type CommandExecutionResult[TInvocation any] struct {
	Ok      bool
	Command TInvocation
	Errors  []string
}

// CommandOptionParseResult holds the result of parsing a single option value.
type CommandOptionParseResult[TValue any] struct {
	Ok    bool
	Value TValue
	Error string
}

// AnyCommandOption is the type-erased interface implemented by all CLI options.
type AnyCommandOption interface {
	OptionName() string
	IsFlag() bool
	IsRepeatable() bool
	parseAny(value string) (any, string, bool)
}

// CommandOptionConfig configures optional option behavior.
type CommandOptionConfig struct {
	Repeatable bool
}

// CommandOption describes a CLI option that parses into TValue.
type CommandOption[TValue any] struct {
	Name       string
	Flag       bool
	Repeatable bool
	Parse      func(value string) CommandOptionParseResult[TValue]
}

// OptionName implements AnyCommandOption.
func (o *CommandOption[TValue]) OptionName() string {
	return o.Name
}

// IsFlag implements AnyCommandOption.
func (o *CommandOption[TValue]) IsFlag() bool {
	return o.Flag
}

// IsRepeatable implements AnyCommandOption.
func (o *CommandOption[TValue]) IsRepeatable() bool {
	return o.Repeatable
}

func (o *CommandOption[TValue]) parseAny(value string) (any, string, bool) {
	res := o.Parse(value)
	if !res.Ok {
		return nil, res.Error, false
	}
	return res.Value, "", true
}

// ValueOption defines a typed CLI option with a custom value parser.
func ValueOption[TValue any](
	name string,
	parse func(value string) CommandOptionParseResult[TValue],
	options ...*CommandOptionConfig,
) *CommandOption[TValue] {
	repeatable := false
	if len(options) > 0 && options[0] != nil {
		repeatable = options[0].Repeatable
	}
	return &CommandOption[TValue]{
		Name:       name,
		Repeatable: repeatable,
		Parse:      parse,
	}
}

// StringOption defines a CLI option that accepts any non-empty string value.
func StringOption(name string, options ...*CommandOptionConfig) *CommandOption[string] {
	return ValueOption(name, func(value string) CommandOptionParseResult[string] {
		return CommandOptionParseResult[string]{Ok: true, Value: value}
	}, options...)
}

// FlagOption defines a boolean flag option that takes no value.
func FlagOption(name string) *CommandOption[bool] {
	return &CommandOption[bool]{
		Name: name,
		Flag: true,
		Parse: func(_ string) CommandOptionParseResult[bool] {
			return CommandOptionParseResult[bool]{Ok: true, Value: true}
		},
	}
}

// ParsedCommandInput exposes parsed option values and remaining positional/unrecognized arguments.
type ParsedCommandInput struct {
	RemainingArgs []string
	values        map[string][]any
}

// OptionValue returns the first parsed value for option, if present.
func OptionValue[TValue any](input *ParsedCommandInput, option *CommandOption[TValue]) (TValue, bool) {
	var zero TValue
	if input == nil || option == nil {
		return zero, false
	}
	raw := input.values[option.Name]
	if len(raw) == 0 {
		return zero, false
	}
	val, ok := raw[0].(TValue)
	if !ok {
		return zero, false
	}
	return val, true
}

// OptionValues returns all parsed values for a repeatable option.
func OptionValues[TValue any](input *ParsedCommandInput, option *CommandOption[TValue]) []TValue {
	if input == nil || option == nil {
		return nil
	}
	raw := input.values[option.Name]
	if len(raw) == 0 {
		return nil
	}
	out := make([]TValue, 0, len(raw))
	for _, entry := range raw {
		if typed, ok := entry.(TValue); ok {
			out = append(out, typed)
		}
	}
	return out
}

// CommandBuildResult is returned by a command's build callback.
type CommandBuildResult[TInvocation any] struct {
	Ok      bool
	Command TInvocation
	Errors  []string
}

type commMutableParsedInput struct {
	values        map[string][]any
	remainingArgs []string
	errors        []string
}

// CommandBuilder turns ParsedCommandInput into a validated command invocation.
type CommandBuilder[TInvocation any] func(input *ParsedCommandInput) CommandBuildResult[TInvocation]

// CommandAction executes a validated command invocation against a context.
type CommandAction[TInvocation any, TContext any] func(command TInvocation, context TContext) error

// AnyCommand allows parent commands to register heterogeneous typed subcommands.
type AnyCommand interface {
	CommandName() string
	parseAny(argv []string) CommandParseResult[NamedCommandInvocation]
	executeAny(argv []string, context any) (CommandExecutionResult[NamedCommandInvocation], error)
}

// Command represents an experimental CLI command or command group.
type Command[TOwnInvocation NamedCommandInvocation, TContext any, TInvocation NamedCommandInvocation] struct {
	Name          string
	options       map[string]AnyCommandOption
	subcommands   map[string]AnyCommand
	builder       CommandBuilder[TOwnInvocation]
	commandAction CommandAction[TOwnInvocation, TContext]
}

// NewCommand creates a named Command.
func NewCommand[TOwnInvocation NamedCommandInvocation, TContext any, TInvocation NamedCommandInvocation](name string) *Command[TOwnInvocation, TContext, TInvocation] {
	return &Command[TOwnInvocation, TContext, TInvocation]{
		Name:        name,
		options:     make(map[string]AnyCommandOption),
		subcommands: make(map[string]AnyCommand),
	}
}

// CommandName implements AnyCommand.
func (c *Command[TOwnInvocation, TContext, TInvocation]) CommandName() string {
	return c.Name
}

// Option registers a CLI option on the command. It panics on duplicate option names.
func (c *Command[TOwnInvocation, TContext, TInvocation]) Option(option AnyCommandOption) *Command[TOwnInvocation, TContext, TInvocation] {
	name := option.OptionName()
	if _, exists := c.options[name]; exists {
		panic(fmt.Sprintf("Option %s is already registered for %s", name, c.Name))
	}
	c.options[name] = option
	return c
}

// Build sets the command's invocation builder.
func (c *Command[TOwnInvocation, TContext, TInvocation]) Build(builder CommandBuilder[TOwnInvocation]) *Command[TOwnInvocation, TContext, TInvocation] {
	c.builder = builder
	return c
}

// Action sets the command's execution handler.
func (c *Command[TOwnInvocation, TContext, TInvocation]) Action(action CommandAction[TOwnInvocation, TContext]) *Command[TOwnInvocation, TContext, TInvocation] {
	c.commandAction = action
	return c
}

// AddSubcommand registers a child subcommand on this command group. It panics on duplicate names.
func (c *Command[TOwnInvocation, TContext, TInvocation]) AddSubcommand(sub AnyCommand) *Command[TOwnInvocation, TContext, TInvocation] {
	name := sub.CommandName()
	if _, exists := c.subcommands[name]; exists {
		panic(fmt.Sprintf("Command %s is already registered", name))
	}
	c.subcommands[name] = sub
	return c
}

// Parse parses argv, dispatching into a matching subcommand if present.
func (c *Command[TOwnInvocation, TContext, TInvocation]) Parse(argv []string) CommandParseResult[TInvocation] {
	if sub, rest, ok := c.selectSubcommand(argv); ok {
		res := sub.parseAny(rest)
		var typed TInvocation
		if res.Ok && res.Command != nil {
			typed, _ = res.Command.(TInvocation)
		}
		return CommandParseResult[TInvocation]{
			Ok:      res.Ok,
			Command: typed,
			Errors:  res.Errors,
		}
	}
	res := c.parseOwn(argv)
	var typed TInvocation
	if res.Ok {
		if cast, ok := any(res.Command).(TInvocation); ok {
			typed = cast
		}
	}
	return CommandParseResult[TInvocation]{
		Ok:      res.Ok,
		Command: typed,
		Errors:  res.Errors,
	}
}

// Execute parses argv and invokes the selected command's action against context.
func (c *Command[TOwnInvocation, TContext, TInvocation]) Execute(argv []string, context TContext) (CommandExecutionResult[TInvocation], error) {
	if sub, rest, ok := c.selectSubcommand(argv); ok {
		res, err := sub.executeAny(rest, context)
		if err != nil {
			return CommandExecutionResult[TInvocation]{}, err
		}
		var typed TInvocation
		if res.Ok && res.Command != nil {
			typed, _ = res.Command.(TInvocation)
		}
		return CommandExecutionResult[TInvocation]{
			Ok:      res.Ok,
			Command: typed,
			Errors:  res.Errors,
		}, nil
	}

	parsed := c.parseOwn(argv)
	if !parsed.Ok {
		return CommandExecutionResult[TInvocation]{
			Ok:     false,
			Errors: parsed.Errors,
		}, nil
	}
	if c.commandAction == nil {
		return CommandExecutionResult[TInvocation]{}, fmt.Errorf("Command %s does not define an action", c.Name)
	}
	if err := c.commandAction(parsed.Command, context); err != nil {
		return CommandExecutionResult[TInvocation]{}, err
	}
	var typed TInvocation
	if cast, ok := any(parsed.Command).(TInvocation); ok {
		typed = cast
	}
	return CommandExecutionResult[TInvocation]{
		Ok:      true,
		Command: typed,
	}, nil
}

func (c *Command[TOwnInvocation, TContext, TInvocation]) parseAny(argv []string) CommandParseResult[NamedCommandInvocation] {
	res := c.Parse(argv)
	return CommandParseResult[NamedCommandInvocation]{
		Ok:      res.Ok,
		Command: res.Command,
		Errors:  res.Errors,
	}
}

func (c *Command[TOwnInvocation, TContext, TInvocation]) executeAny(argv []string, rawContext any) (CommandExecutionResult[NamedCommandInvocation], error) {
	typedCtx, ok := rawContext.(TContext)
	if !ok {
		return CommandExecutionResult[NamedCommandInvocation]{}, fmt.Errorf("Command %s received incompatible context", c.Name)
	}
	res, err := c.Execute(argv, typedCtx)
	if err != nil {
		return CommandExecutionResult[NamedCommandInvocation]{}, err
	}
	return CommandExecutionResult[NamedCommandInvocation]{
		Ok:      res.Ok,
		Command: res.Command,
		Errors:  res.Errors,
	}, nil
}

func (c *Command[TOwnInvocation, TContext, TInvocation]) selectSubcommand(argv []string) (AnyCommand, []string, bool) {
	if len(argv) == 0 {
		return nil, nil, false
	}
	sub, ok := c.subcommands[argv[0]]
	if !ok {
		return nil, nil, false
	}
	return sub, argv[1:], true
}

func (c *Command[TOwnInvocation, TContext, TInvocation]) parseOwn(argv []string) CommandParseResult[TOwnInvocation] {
	if c.builder == nil {
		panic(fmt.Sprintf("Command %s does not define a builder", c.Name))
	}
	parsed := c.parseOptions(argv)
	input := &ParsedCommandInput{
		RemainingArgs: parsed.remainingArgs,
		values:        parsed.values,
	}
	built := c.builder(input)
	errors := make([]string, 0, len(parsed.errors)+len(built.Errors))
	errors = append(errors, parsed.errors...)
	if !built.Ok {
		errors = append(errors, built.Errors...)
	}
	if len(errors) > 0 {
		return CommandParseResult[TOwnInvocation]{
			Ok:     false,
			Errors: errors,
		}
	}
	if !built.Ok {
		panic(fmt.Sprintf("Command %s failed without an error", c.Name))
	}
	return CommandParseResult[TOwnInvocation]{
		Ok:      true,
		Command: built.Command,
	}
}

func (c *Command[TOwnInvocation, TContext, TInvocation]) parseOptions(argv []string) commMutableParsedInput {
	parsed := commMutableParsedInput{
		values: make(map[string][]any),
	}
	for index := 0; index < len(argv); index++ {
		argument := argv[index]
		if argument == "--" {
			parsed.remainingArgs = append(parsed.remainingArgs, argv[index:]...)
			break
		}

		equals := strings.IndexByte(argument, '=')
		name := argument
		if equals != -1 {
			name = argument[:equals]
		}
		option, ok := c.options[name]
		if !ok {
			parsed.remainingArgs = append(parsed.remainingArgs, argv[index:]...)
			break
		}

		var value string
		if option.IsFlag() {
			if equals != -1 {
				parsed.errors = append(parsed.errors, fmt.Sprintf("%s does not take a value", name))
				continue
			}
			value = ""
		} else {
			hasCandidate := false
			candidate := ""
			if equals != -1 {
				candidate = argument[equals+1:]
				hasCandidate = true
			} else if index+1 < len(argv) {
				next := argv[index+1]
				if !strings.HasPrefix(next, "-") {
					candidate = next
					hasCandidate = true
					index++
				}
			}
			if !hasCandidate || candidate == "" {
				parsed.errors = append(parsed.errors, fmt.Sprintf("%s requires a value", name))
				continue
			}
			value = candidate
		}

		values := parsed.values[name]
		if len(values) > 0 && !option.IsRepeatable() {
			parsed.errors = append(parsed.errors, fmt.Sprintf("%s may only be specified once", name))
			continue
		}
		parsedVal, errMsg, parseOk := option.parseAny(value)
		if !parseOk {
			parsed.errors = append(parsed.errors, errMsg)
			continue
		}
		parsed.values[name] = append(values, parsedVal)
	}
	return parsed
}

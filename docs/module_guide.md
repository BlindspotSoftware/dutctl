# [DRAFT / OUTDATED] How to implement a new module

## The interface
```
type Module interface {
	Help() string
	Init() error
	Run(ctx Context, args ...string) error
	ValidateOptions(opts map[string]any) error
}
```

Every module needs to satisfy the Module interface, hence must implement these functions.

### The Help()-Function
This function shall return a formatted string with the capabilities the module provides.
Basically the user receives all required information to interact with the module.

### The Init(opts map[string]any)-Function
This function is called, when the module is loaded by dutagent and sets up the environment for the module to run.
The map contains the validated options from the configuration file and is used to fill the actual Options structure.
If the module does not implement Options, the parameter name `opts` must be replaced by an underscore `_`.

### The Run(ctx Context, args ...string)-Function
This function is executed, when the user interacts with the module via dutctl.
It runs for as long as it may take to finish the job of the module.
Some modules require asynchronous communication with the device under test(DUT) until certain conditions are met or dutctl terminates.
If dutctl terminates, a signal can be received via the context and shut down the execution of the module.

### Option struct and the ValidateOptions(opts map[string]any)-Function
If the module requires configuration data, these can be provided by the configuration file.
An Options structure in the module reflects this capability and can look like the following:
```
type Options struct{
	Opt1 Type1 `yaml:"opt1 required:""`
	Opt2 Type2 `yaml:"opt2 required:""`
	Opt3 Type3 `yaml:"opt3`
}
```
The ValidateOptions-function will validate the configuration file against the Options structure.
Fields of the structure must be tagged for yaml-parsing.

```
E.g.: Opt1 Type1 `yaml:"opt1"`
```
An additional tag can mark the option as required.
This Option MUST be provided by the configuration file, for its information is required for proper functionality of the module.
```
E.g.: Opt1 Type1 `yaml:"opt1" required:""`
```

### Context capabilities
```
type Context struct {
	context.Context

	console ConsoleHandler
	file    FileHandler
	log     LogHandler
	print   PrintHandler
	term    TerminalHandler
}
```

The module.Context holds handlers for different capabilities every module has access to.
The ConsoleHandler provides means to pipe data from the serial of the DUT to DUTCtl and vice versa.
At the moment FileHandler is a placeholder and will hold capabilties to request a file transfer from DUTCtl to DUTAgent in the future.
TerminalHandler gives capability to set the Terminal on side of the DUTCtl into RAW MODE for proper data transmission to the serial of a DUT.

## Example
The dummy-button module provides a good example for the implementation of a module:
```
package dummybutton

import (
	"reflect"

	"/ropo/paath/dutctl/module"
)

type Button struct {
	Options `yaml:"options"`
}

type Options struct {
	Opt1 string `yaml:"opt1" required:""`
	Opt2 string `yaml:"opt2" required:""`
	Opt3 string `yaml:"opt3"`
}

func (m *Button) Help() string {
	return "button"
}

func (m *Button) Init(_ map[string]any) error {
	return nil
}

func (m *Button) Run(ctx module.Context, _ ...string) error {
	ctx.Debug("dummy-button: button pressed")

	return nil
}

func (m *Button) ValidateOptions(opts map[string]any) error {
	return module.CompareOptions(opts, reflect.ValueOf(Options{}))
}
```

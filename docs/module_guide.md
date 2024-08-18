# Module Plug-In System

In DUT Control, modules represent the implementation of actions to be performed on a device. One or more Modules make up a command that can be issued to a device. The implementation of a Module determines its capabilities and also exposes information on how to use and configure it.

The DUT Control project is designed for easy integration of new modules via a plug-in system.

> [!NOTE] 
> The modules generally available at a running dutagent instance are set at compile time of dutagent. 
> Which Modules are use with certain devices is controlled via the dutagent configuration, when starting dutagent.

## The Module interface
Modules must implement the following interface:
```
type Module interface {
	Help() string
	Init() error
	Deinit() error
	Run(ctx context.Context, s Session, args ...string) error
}
```
See [`pkg/module/module.go`](../pkg/module/module.go) for further information on the set of functions. 
With the _Session_ provided to the module, it is able to interact with the client during execution (status messages, request input, file transfer, etc.).

## Registration

New modules go under `pkg/modules'. 

To register a module for use in dutagend, modules must call `module.Register()` and provide its name and a constructor. By convention, this is done in the module's `init()` function. E.g:
```go
func init() {
	module.Register(module.Info{
		ID:  "reset",
		New: func() module.Module { return new(power.Reset) },
	})
}
```
`ID` is the module's unique identifier. This string is used in the [dutagent configuration](./dutagent-config.md) to refer to this module implementation. 

`New` is a function to instantiate an instance of the module. Usually it can be as simple as shown above. 
Note that initial setup code can be placed in the `Init()` function of the module interface, which supports error checking and should be preferred over the constructor for most setup code. 

## Configuration
A module can be dynamically configured when starting a dutagent using the options map in the [dutagent configuration](./dutagent-config.md#module). A module must be of type `struct' and have the options as fields. The parser will set the struct fields to match the map keys.

For example, a module like the one below, registered with `ID` = `"my-module"`.
```go
type MyModule struct {
	Foo int    
}
```
can be configured with:
```yaml
---
version: 0
devices:
  some-device:
    desc: Example device
    cmds:
      some-cmd:
        desc: My cool module
        modules:
          - module: my-module
            options:
              foo: 42
```
> [!IMPORTANT]  
> It is imperative that the module's documentation and Help() function provide a good explanation of its options.
> The option map in the configuration file is generic (string -> any type), so it is important that the user knows what values are expected.


The [project's dummy modules](../pkg/module/dummy/dummy_status.go) show all the details of a complete implementation.


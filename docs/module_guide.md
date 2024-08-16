# Module Plug-In System

In DUT Control, _Modules_ represent the implementation of actions to perform on a DUT. One or many Modules form a Command that can be issued on a device. The implementation of a Module determines its capabilities and also exposes information on how to use und configure it.  

The DUT Control project is designed for easy integration of new Modules via a plug-in system.

> [!NOTE] 
> The Modules generally available at a running dutagent instance are set at compile time of dutagent. 
> Which Modules are use with certain devices is controlled via the dutagent configuration, when starting dutagent.

## The Module interface
Modules must implement the Module interface (`pkg/module/module.go`)
```
type Module interface {
	Help() string
	Init() error
	Deinit() error
	Run(ctx context.Context, s Session, args ...string) error
}
```
See [`pkg/module/module.go`](../pkg/module/module.go) for further information on the set of functions. 
With the _Session_ provided to the module, it is capabal of interact with the client during execution (status messages, request input, file transfer, etc.).

## Registration

New Modules go under `pkg/modules`. 

In order to register a module for use in dutagend, modules need to call `module.Register() and provide its name and a constructor. By convention this is done in the modules `init` function. E.g.:
```go
func init() {
	module.Register(module.Info{
		ID:  "reset",
		New: func() module.Module { return new(power.Reset) },
	})
}
```
`ID` is the Module's unique identifier. This string is used in the [dutagent configuration](./dutagent-config.md) to refer to this module implementation. 

`New` is a function to instantiate an instance of the Module. Usually it can be as straight forward as shown above. 
Keep in mind that initial setup code can be placed in the Modules `Init()` function, which serves error checking and should thus be prefered over the constructor for most setup code. 

## Configuration
A Module can be configured dynamically when starting a dutagend via the _options_ map in the [dutagent configuration](./dutagent-config.md#module). Therefor a module must be of type `struct`. The parser will set the struct fields matching the maps keys. 

E.g. a module like below, witch registers itself with `ID` = `"my-module"`
```go
type MyModule struct {
	Foo   int    
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
> It is mandatory that the Module's documentation and Help() function provide good explanation about its options.
> The option map is generic (string -> any type), so it is important that the uer knows what values are expected.


The [project's dummy modules](../pkg/module/dummy/dummy_status.go) show all details of a complete implementation.




# [DRAFT] How to implement a new module

## The interface
```
type Module interface {
	Help() string
	Init() error
	Run(ctx Context, args ...string) error
}
```

Every module needs to satisfy the Module interface, hence must implement these functions.

### The Help()-Function
This function shall return a formatted string with the capabilities the module provides.
Basically the user receives all required information to interact with the module.

### The Init(opts map[string]any)-Function
This function is called, when the module is loaded by DUT Agent and sets up the environment for the module to run.
The map contains the validated options from the configuration file.

### The Run(ctx Context, args ...string)-Function
This function is executed, when the user interacts with the module via dutctl.
It runs for as long as it may take to finish the job of the module.
Some modules require asynchronous communication with the DUT until certain conditions are met or dutctl terminates.


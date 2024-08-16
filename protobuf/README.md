# Code Generation

The code for the Remote Procedure Calls (RPCs) is generated by with buf. See [1](https://connectrpc.com/docs/go/getting-started#generate-code) for more information and installation instructions. 

The version used here is `buf v1.34.0`. 

After editing `dutctl/v1/dutctl.proto` update the generated code: 
```
buf lint
buf generate
```
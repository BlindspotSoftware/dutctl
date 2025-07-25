# DUT Control
Device-under-Test (DUT) Control is an abstraction layer for remote hardware access.

For details on the system architecture see [docs](./docs).

| Supported Client OS | Supported DUT Agent Hardware |
| ------------------- | ---------------------------- |
| Linux               | RaspberryPi 4                |

| Modules                                                           | Status             |
| ----------------------------------------------------------------- | ------------------ |
| [GPIO Button](./pkg/module/gpio/README.md)                        | :white_check_mark: |
| [GPIO Switch](./pkg/module/gpio/README.md)                        | :white_check_mark: |
| [IPMI Power Control](./pkg/module/gpio/README.md)                 | :white_check_mark: |
| [Power Distribution Unit, Intellinet](./pkg/module/pdu/README.md) | :white_check_mark: |
| Power Distribution Unit, Delock                                   | :x:                |
| [SPI Flasher, flashrom](./pkg/module/flash/README.md)             | :white_check_mark: |
| SPI Flasher, flashprog                                            | :x:                |
| [Serial Console](./pkg/module/serial/README.md)                   | :white_check_mark: |
| [Shell Execution](./pkg/module/shell/README.md)                   | :white_check_mark: |
| [Secure Shell (SSH)](./pkg/module/ssh/README.md)                  | :white_check_mark: |



# Roadmap
This project is in it's kickoff phase. Beta-Versions will be released onece the initial system architecture is set up and and the first module is implemented. More modules will then follow in further beta versions until a set of features is supported to control a DUT for a basic development cycle. See the project's [milstones](https://github.com/BlindspotSoftware/dutctl/milestones?direction=asc&sort=due_date&state=open) for more details.

# Contributing
Until MVP is finished, external contributions most likely won't be handled.

--------
This project is supported by

![image](https://github.com/user-attachments/assets/1237fcaa-b3c3-4031-afac-34d789e8c096)


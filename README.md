# DUT Controll
Device-under-Test (DUT) Control is an abstraction layer for remote hardware access.

For details on the system architecture see [docs](./docs).

| Supported Client OS | Supported DUT Agent Hardware |
|---------------------|------------------------------|
|  Linux | RaspberryPi 4 (planned :hourglass:)|

| Modules | Status |
|-------------------|--------|
| GPIO Button       | :x:|
| GPIO Switch       | :x:|
| IPMI Power Control | :x:|
| Power Distribution Unit, Intellinet       | :x:|
| Power Distribution Unit, Delock       | :x:|
| SPI Flasher, dediprog       | :x:|
| SPI Flasher, flashrom       | :x:|
| SPI Flasher, flashprog       | :x:|
| SPI Flasher, em100       | :x:|
| Serial Console       | :x:|
| Shell Execution       | :x:|
| Secure Shell (SSH)       | :x:|



# Raodmap
This project is in it's kickoff phase. Beta-Versions will be released onece the initial system architecture is set up and and the first module is implemented. More modules will then follow in further beta versions until a set of features is supported to control a DUT for a basic development cycle. See the project's [milstones](https://github.com/BlindspotSoftware/dutctl/milestones?direction=asc&sort=due_date&state=open) for more details.

# Contributing
Until MVP is finished, external contributions most likely won't be handled.

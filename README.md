# dutctl
DUT Control is an abstraction layer for remote hardware access.

For details on the system architecture see [docs](./docs).

| Supported DUT Agent Hardware | Status |
|--------------------|--------|
| RaspberryPi 4      | planned :hourglass:|

| Supported Client OS | Status |
|--------------------|--------|
| Linux              | planned :hourglass:|

| Supported Modules | Status |
|-------------------|--------|
| GPIO Button       | wip :x:|
| GPIO Switch       | wip :x:|
| IPMI Power Control | wip :x:|
| Power Distribution Unit, Intellinet       | wip :x:|
| Power Distribution Unit, Delock       | wip :x:|
| SPI Flasher, dediprog       | wip :x:|
| SPI Flasher, flashrom       | wip :x:|
| SPI Flasher, flashprog       | wip :x:|
| SPI Flasher, em100       | wip :x:|
| Serial Console       | wip :x:|
| Shell Execution       | wip :x:|
| Secure Shell (SSH)       | wip :x:|



# Raodmap
This project is in it's kickoff phase. Beta-Versions will be released onece the initial system architecture is set up and and the first module is implemented. More modules will then follow in further beta versions until basic features are supported to control a DUT for a basic development cycle.  

- [ ] Set up system architecture
- [ ] Implement communication layer
- [ ] Implement configurable module system
- [ ] Release 1st beta version

# Contributing
Until MVP is finished, external contributions most likely won't be handled.

# Changelog

## [2.0.0-alpha.1](https://github.com/BlindspotSoftware/dutctl/compare/v1.0.0-alpha.1...v2.0.0-alpha.1) (2026-02-26)


### ⚠ BREAKING CHANGES

* The dutagent configuration changed. Existing configurations must be updated in the following way: From commands:   - name: example     modules:       - name: flash         options:           file: firmware.bin To commands:   - name: example     uses:       - name: flash         with:           file: firmware.bin
* Flash module removed default tool. Users must now explicitly configure the tool option in their flash module configuration.
* The -v flag now enabled verbose output, while version information is printed with the 'dutctl version' command.
* The client does not add an extra newline to received Print messages anymore.

### Features

* add dpcmd support ([13a4ace](https://github.com/BlindspotSoftware/dutctl/commit/13a4acee09848ff7458bb4eb7bf7dfbdbbd07109))
* add emulate module for em100 support ([268bb80](https://github.com/BlindspotSoftware/dutctl/commit/268bb80c75e0c8d398d0e6270e14165b8b4cbd86))
* add file module for bidirectional file transfers ([ded8a2c](https://github.com/BlindspotSoftware/dutctl/commit/ded8a2cf7bc96061d743df511bb3060c47a087f6))
* add flashprog support to flash module ([7a9c309](https://github.com/BlindspotSoftware/dutctl/commit/7a9c30990ebce4ad84b3dbddbf5677c01e9823ad))
* add further output options to dutctl ([5c027cc](https://github.com/BlindspotSoftware/dutctl/commit/5c027ccb170f37eb35a64ac16596872674ba936d))
* add wifi socket module ([329161d](https://github.com/BlindspotSoftware/dutctl/commit/329161d5b3834edc5b871741e5feea059a350b48))
* dutctl verbose output flag ([abb8d67](https://github.com/BlindspotSoftware/dutctl/commit/abb8d67afcfc56a7d713a9c64e2329503d5340f4))
* establish reusable test doubles ([62a5c99](https://github.com/BlindspotSoftware/dutctl/commit/62a5c990e7b69f73aa1126b5dfb89b406a51b66a))
* extend module.Session interface  with fmt-style print functions ([3432675](https://github.com/BlindspotSoftware/dutctl/commit/34326753a2a6afc4722329d5e87a8ca6caa89ba4))


### Bug Fixes

* broker does not treat io.EOF as error ([1588898](https://github.com/BlindspotSoftware/dutctl/commit/15888988dfdfef39270b3db5efbb31dc8b334042))
* defer port opening to runtime in serial module ([fd417af](https://github.com/BlindspotSoftware/dutctl/commit/fd417af170850aec61e40d4bf74376b9b2d09124))
* relay flash tool stdout/stderr to client ([78a623c](https://github.com/BlindspotSoftware/dutctl/commit/78a623cef29f786b2a158cb4262a4d34e4dcf601))
* remove extra newline for print messages ([afe31fe](https://github.com/BlindspotSoftware/dutctl/commit/afe31fe40513d431ab9ecf5cef24c9620d332255))


### Documentation

* add missing modules to README and fix IPMI link ([b79eff2](https://github.com/BlindspotSoftware/dutctl/commit/b79eff2064daffff52eea72c10a7898fe6ea9876))
* explain module.Session interface ([3e9612e](https://github.com/BlindspotSoftware/dutctl/commit/3e9612eb677d904b3ea634c0d6a5f28e1afabcaf))
* fix several linting issues in markdown files ([1db2cfa](https://github.com/BlindspotSoftware/dutctl/commit/1db2cfa0a3890ba73bf7499a5a28ab73c63fd4ae))
* fix typos in stream and test fakes ([52d7e32](https://github.com/BlindspotSoftware/dutctl/commit/52d7e323ed8e4565bf2be5665ef082a63eeab684))
* fix typos, spelling and tables ([c1dbf9b](https://github.com/BlindspotSoftware/dutctl/commit/c1dbf9b840f91a3c6f22332a704839fe2374e2c6))
* sanitize modules example configuration ([0479c66](https://github.com/BlindspotSoftware/dutctl/commit/0479c66eb96ab7e94208280eb87569779a834768))


### Other Work

* broker worker lifecycle and error channel handling ([1da20f9](https://github.com/BlindspotSoftware/dutctl/commit/1da20f96be24b0506741e9960ca914920f60a722))
* broker.Start returns session and error channel ([b970bac](https://github.com/BlindspotSoftware/dutctl/commit/b970bac4554a4b4b087b44661b464866e123a760))
* bump actions/checkout from 4 to 5 ([ab84c81](https://github.com/BlindspotSoftware/dutctl/commit/ab84c8109330b5bea1247ebbe161d71e6ea442ff))
* bump actions/checkout from 5 to 6 ([3bb8551](https://github.com/BlindspotSoftware/dutctl/commit/3bb8551a2ef06a77ddd8b24f8e3ee00825d17714))
* bump actions/setup-go from 5 to 6 ([77b3beb](https://github.com/BlindspotSoftware/dutctl/commit/77b3beb4336d393ec08bcecfd3c38d44aa0af35e))
* bump actions/setup-node from 4 to 6 ([bd4e5f7](https://github.com/BlindspotSoftware/dutctl/commit/bd4e5f71f9ab340a78ef00f90b846adb77f75058))
* bump connectrpc.com/connect from 1.18.1 to 1.19.1 ([323b013](https://github.com/BlindspotSoftware/dutctl/commit/323b013a9fab7357433e676ee2fa96aead0c8ee5))
* bump github.com/bougou/go-ipmi from 0.7.7 to 0.7.8 ([d561eab](https://github.com/BlindspotSoftware/dutctl/commit/d561eab15674ea30f35f9667500bbf1d3a57f4d2))
* bump github.com/go-playground/validator/v10 ([cabf73d](https://github.com/BlindspotSoftware/dutctl/commit/cabf73de494174285fbfd31428563d333b0aca78))
* bump github.com/go-playground/validator/v10 ([3f39f64](https://github.com/BlindspotSoftware/dutctl/commit/3f39f64ad88e227f90fcc05c37424d45704d40d6))
* bump github.com/go-playground/validator/v10 ([2293e9d](https://github.com/BlindspotSoftware/dutctl/commit/2293e9d6948eaf7221a0d5ba953ab1ef0c9c952b))
* bump golang.org/x/crypto ([933701c](https://github.com/BlindspotSoftware/dutctl/commit/933701cdba424f312f490806e79d53038362d70b))
* bump golang.org/x/crypto from 0.45.0 to 0.46.0 ([4f262d8](https://github.com/BlindspotSoftware/dutctl/commit/4f262d8b8bc25e61345078e7ea9899bda7871021))
* bump golang.org/x/crypto from 0.46.0 to 0.47.0 ([e75f000](https://github.com/BlindspotSoftware/dutctl/commit/e75f00066bf5534b11053fedea87a3d44504f39d))
* bump golang.org/x/net from 0.42.0 to 0.46.0 ([f11049e](https://github.com/BlindspotSoftware/dutctl/commit/f11049e60a6e014d8468f439bee386e68daafac7))
* bump golang.org/x/net from 0.46.0 to 0.47.0 ([da28691](https://github.com/BlindspotSoftware/dutctl/commit/da286913efed73be4d040ec932698ab4bb911474))
* bump golang.org/x/net from 0.47.0 to 0.48.0 ([bc399bd](https://github.com/BlindspotSoftware/dutctl/commit/bc399bd9f1bcc4c50d2598c446f30bd543c851e4))
* bump golang.org/x/net from 0.48.0 to 0.49.0 ([6e3aaa0](https://github.com/BlindspotSoftware/dutctl/commit/6e3aaa0a4fad081134579c208e528b2c004a6ae6))
* bump golang.org/x/net from 0.49.0 to 0.50.0 ([2dd0cbb](https://github.com/BlindspotSoftware/dutctl/commit/2dd0cbbca02b8546fe31d46217d687594b933c63))
* bump golangci/golangci-lint-action from 8 to 9 ([244cb31](https://github.com/BlindspotSoftware/dutctl/commit/244cb3153fe60f6311163cd0ccb074a93a37c707))
* bump google.golang.org/protobuf from 1.36.10 to 1.36.11 ([4739d6b](https://github.com/BlindspotSoftware/dutctl/commit/4739d6b4f3e0b1c1739d0e67662aa8ff09c2b560))
* bump google.golang.org/protobuf from 1.36.6 to 1.36.10 ([e49cc65](https://github.com/BlindspotSoftware/dutctl/commit/e49cc6559ee3084f4aaf2093654044f96bf71e88))
* change naming of dutagent config items ([f795b4c](https://github.com/BlindspotSoftware/dutctl/commit/f795b4cb3e45a48372265af8d6c28bb043693d32))
* cover dutagent/states ([b7ea4c4](https://github.com/BlindspotSoftware/dutctl/commit/b7ea4c4fe19648cf6214c8f32f9dad9a8103c90b))
* defer moduleErrCh channel init to FSM execution state ([4c01fd0](https://github.com/BlindspotSoftware/dutctl/commit/4c01fd04a705626201846e7d3b8062e07bd5e562))
* dutctl output formatter ([923f673](https://github.com/BlindspotSoftware/dutctl/commit/923f673ad62424d6aabc0833dc76f8a918a40705))
* early ctx cancellation in Run RPC ([7638b3b](https://github.com/BlindspotSoftware/dutctl/commit/7638b3ba86322400dda98e00a3f52668ae5ff65b))
* fix flaky TestWaitModules by resolving race condition ([b98a7e3](https://github.com/BlindspotSoftware/dutctl/commit/b98a7e3c5e91e928540efd1eae0576fad8b70f3d))
* fix typo and improve logging in rpc handler ([53cac3a](https://github.com/BlindspotSoftware/dutctl/commit/53cac3a14cf7fb2d3acd5747ffbcdb567d4244be))
* group dutagent FSM states in own file ([f1014a4](https://github.com/BlindspotSoftware/dutctl/commit/f1014a4cca351d65338f7d0b1485d99d264d2d5e))
* improve default output of dutctl ([46817ce](https://github.com/BlindspotSoftware/dutctl/commit/46817ce15923363133216170ab1d2b5b92af6199))
* modules use session.Print/f/ln functions ([a5acb8f](https://github.com/BlindspotSoftware/dutctl/commit/a5acb8fca7a7499bbc7af76c03e47ad63e791c19))
* remove check if filepath on dutctl matches with args ([9f625a4](https://github.com/BlindspotSoftware/dutctl/commit/9f625a41a4fe4c28300fc8ff886e9bfe4b5abaa3))
* resolve busy waiting in waitModules loop ([13b2ef7](https://github.com/BlindspotSoftware/dutctl/commit/13b2ef75ea3d111019c80da3ad259faf8de70f84))
* send/receive synchronization in dutclient ([6f900f2](https://github.com/BlindspotSoftware/dutctl/commit/6f900f28bc6ebe01393a5972acdcfbf63aa27ba8))
* tune .gitignore ([4e9c943](https://github.com/BlindspotSoftware/dutctl/commit/4e9c94318d254e20a45278012c6c0831e62a16fd))
* unify broker and module channels to error-only pattern ([84e39d1](https://github.com/BlindspotSoftware/dutctl/commit/84e39d13f64aa4e7d11e4430168ad5e129925852))
* use constants for flag usage strings ([ceb0bf6](https://github.com/BlindspotSoftware/dutctl/commit/ceb0bf68656752b7bee4139b33748e203364eca9))
* use dutagent.Stream as project wide RPC stream abstraction ([7c0fa17](https://github.com/BlindspotSoftware/dutctl/commit/7c0fa171608b56220f7617afdc15cbf7662fdf48))

## [1.0.0-alpha.1](https://github.com/BlindspotSoftware/dutctl/compare/v0.10.0...v1.0.0-alpha.1) (2025-07-27)


### Documentation

* clarify args field of dutagent config ([f7c0dca](https://github.com/BlindspotSoftware/dutctl/commit/f7c0dca49db11f39373b18136d940008fb9c464a))
* update README for pre-release ([e438733](https://github.com/BlindspotSoftware/dutctl/commit/e43873318484723198e5bb4ae2063237aa28f15e))


### Other Work

* add blank issue templates ([8b33468](https://github.com/BlindspotSoftware/dutctl/commit/8b33468a532558abb8a6d18bee30615c4eea8ba4))
* add CODE_OF_CONDUCT.md ([9c273fa](https://github.com/BlindspotSoftware/dutctl/commit/9c273faee5e93eb9949e3f29659e754f5b1bb431))
* add GOVERNANCE.md ([d02511e](https://github.com/BlindspotSoftware/dutctl/commit/d02511e498270dc5d38e867f3350bc93851770a1))
* add issue template for bugs ([4459842](https://github.com/BlindspotSoftware/dutctl/commit/4459842ec8ff1cbc6b43f6959750a1ff78df1caa))
* add issue templates for feature requests ([a8896b2](https://github.com/BlindspotSoftware/dutctl/commit/a8896b27fe90554c696c5fdbf096328e4f7a9022))
* create SECURITY.md ([c7f7ec1](https://github.com/BlindspotSoftware/dutctl/commit/c7f7ec104394b557fbed8cc7a744c16e7f661da3))
* fix release-please config ([32cf2e3](https://github.com/BlindspotSoftware/dutctl/commit/32cf2e32b02af543cddac769735352a016b0ef0e))
* set pre-release tag manually ([c46e090](https://github.com/BlindspotSoftware/dutctl/commit/c46e090af56ac651955f0bedb957e14109cc3f77))
* set release strategy to alpha pre-release ([a9792f7](https://github.com/BlindspotSoftware/dutctl/commit/a9792f7a24d84ccb149cc794b1287781a5839af7))
* update CONTRIBUTING.md ([8fc6586](https://github.com/BlindspotSoftware/dutctl/commit/8fc658614b09392677fe56eaa4301fcd544a0e8e))
* update DCO check ([49ed28f](https://github.com/BlindspotSoftware/dutctl/commit/49ed28fe720d87fa6da61f8203b33923ded8be4c))
* update license to 2025 ([25667d9](https://github.com/BlindspotSoftware/dutctl/commit/25667d9550fd941b9f5642678ca3d727d2a0b14d))

## [0.10.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.9.0...v0.10.0) (2025-07-20)


### Features

* dutagent provides flag to register with dutserver ([9bab513](https://github.com/BlindspotSoftware/dutctl/commit/9bab51376c634784a9b3622390ba50e4109734be))
* dutserver device registration ([21c1088](https://github.com/BlindspotSoftware/dutctl/commit/21c10882bf3d3539acc1bcc4cff00c79daa7999d))
* dutserver PoC (experimental) ([6b92edf](https://github.com/BlindspotSoftware/dutctl/commit/6b92edf468f480e70ba96e3690aa3d9f1b114fca))
* protobuf RelayService ([a406935](https://github.com/BlindspotSoftware/dutctl/commit/a40693505f9d01f4504d5ee66d9e862491a75aad))


### Documentation

* add DUT server information ([87faf07](https://github.com/BlindspotSoftware/dutctl/commit/87faf07d1c325c44b97e9fe29e5872f7ebeac6d0))

## [0.9.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.8.0...v0.9.0) (2025-07-20)


### Features

* version flag ([c3e04f4](https://github.com/BlindspotSoftware/dutctl/commit/c3e04f40ed993c30dd473230f085475f5cc80282))


### Other Work

* bump connectrpc.com/connect from 1.16.2 to 1.18.1 ([1d0fe99](https://github.com/BlindspotSoftware/dutctl/commit/1d0fe997e550b552a31a554910fca440163638b1))
* bump github.com/bougou/go-ipmi from 0.7.6 to 0.7.7 ([545d85c](https://github.com/BlindspotSoftware/dutctl/commit/545d85c1653bf784925634b7a7596f5b3129fb94))
* bump github.com/go-playground/validator/v10 ([ba24a08](https://github.com/BlindspotSoftware/dutctl/commit/ba24a0811cd26bd1531d7b88addd375ed8c44a4e))
* bump go version to 1.24 ([32aed17](https://github.com/BlindspotSoftware/dutctl/commit/32aed176c8ada72faa160dc5fcd245d13c9b5fdb))
* bump golang.org/x/crypto from 0.39.0 to 0.40.0 ([3948244](https://github.com/BlindspotSoftware/dutctl/commit/3948244842553cec38099cc9b64802a0af6f5b27))
* bump golang.org/x/net from 0.28.0 to 0.41.0 ([d121903](https://github.com/BlindspotSoftware/dutctl/commit/d121903dab7fc62f27bfab87c8efec6d446fbbab))
* bump golang.org/x/net from 0.41.0 to 0.42.0 ([bc911e1](https://github.com/BlindspotSoftware/dutctl/commit/bc911e128badbfe4f8eb033450a530dfc41de864))
* bump google.golang.org/protobuf from 1.34.2 to 1.36.6 ([5afb05a](https://github.com/BlindspotSoftware/dutctl/commit/5afb05ad2010756cf45840e10d6f4a09a1d71f34))
* create dependabot.yml ([828519b](https://github.com/BlindspotSoftware/dutctl/commit/828519be937a8e1b73976dfd1f6d8edf00a3e046))

## [0.8.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.7.0...v0.8.0) (2025-06-26)


### Features

* module flash ([f96db16](https://github.com/BlindspotSoftware/dutctl/commit/f96db1691387e092ccc6e236cba85e3e2a5ac797))

## [0.7.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.6.0...v0.7.0) (2025-06-26)


### Features

* module IPMI ([5b42fd2](https://github.com/BlindspotSoftware/dutctl/commit/5b42fd290880f6fd6e578143a4f524eb0d3ae3d7))
* module PDU ([9a99f2a](https://github.com/BlindspotSoftware/dutctl/commit/9a99f2a04b0299058be969be41c95d71f8ac8d37))


### Documentation

* fix link in README ([f07fbe2](https://github.com/BlindspotSoftware/dutctl/commit/f07fbe2c27a5e48a0915d6c8f0ab4a14e41560a8))
* fix typo in README.md ([a0f9ee0](https://github.com/BlindspotSoftware/dutctl/commit/a0f9ee0d6a569110953c23ced4c21a190cb60844))

## [0.6.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.5.0...v0.6.0) (2025-04-22)


### Features

* module serial ([738c176](https://github.com/BlindspotSoftware/dutctl/commit/738c176840cb8dd6b95935b75b3f12b85149161b))

## [0.5.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.4.0...v0.5.0) (2025-04-19)


### Features

* module ssh ([2adf2b4](https://github.com/BlindspotSoftware/dutctl/commit/2adf2b40bd5f528de78884cd5f05bf4917e485ac))

## [0.4.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.3.1...v0.4.0) (2025-04-03)


### Features

* module shell ([5ba103e](https://github.com/BlindspotSoftware/dutctl/commit/5ba103e0f9fb1e7f5666ea9e6143e7fb10f305ae))

## [0.3.1](https://github.com/BlindspotSoftware/dutctl/compare/v0.3.0...v0.3.1) (2025-03-31)


### Documentation

* add link for switch module in README ([455f4bc](https://github.com/BlindspotSoftware/dutctl/commit/455f4bc080f4069cb43c389bb196ea4b07366779))

## [0.3.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.2.0...v0.3.0) (2025-02-18)


### ⚠ BREAKING CHANGES

* `args` field of module object in dutagent config is now a string array.

### Features

* modules gpio.Button and gpio.Switch ([45d0e17](https://github.com/BlindspotSoftware/dutctl/commit/45d0e1707a00f60bf5f02162c7d7f15c04985679))


### Bug Fixes

* pass arguments to non-main modules as array ([dc2d224](https://github.com/BlindspotSoftware/dutctl/commit/dc2d224f5014cec742dbb207742121fcbc6eab84))


### Documentation

* improve dutagent config reference ([510e364](https://github.com/BlindspotSoftware/dutctl/commit/510e3646aa65e012ca12cfe457120ee1a68da6c0))
* update README for GPIO module support ([c781dc5](https://github.com/BlindspotSoftware/dutctl/commit/c781dc50e16c86bcfff910e5942164f6e8ecdb12))


### Other Work

* add internal/test pkg with module.Session mock ([a1af652](https://github.com/BlindspotSoftware/dutctl/commit/a1af652acec26a5147bdc248ed11578ae9278825))
* improve GPIO module backend abstraction ([7718ddf](https://github.com/BlindspotSoftware/dutctl/commit/7718ddff467e889c2e16b959e3b676172ee8ce55))

## [0.2.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.1.0...v0.2.0) (2024-11-28)


### Features

* module agent.Status ([2635ca8](https://github.com/BlindspotSoftware/dutctl/commit/2635ca871c33a5a794b5c6161a8aaa17e099e212))
* module time.Wait ([3c26adb](https://github.com/BlindspotSoftware/dutctl/commit/3c26adb00086366b7ac9267ba6b76fc7cec01a1e))


### Bug Fixes

* quote module name in error message of module registration ([ad52156](https://github.com/BlindspotSoftware/dutctl/commit/ad521569755115b3af3bf165346d301a094181a9))
* remove pointer field in dut.ModuleConfig ([ff7cb6f](https://github.com/BlindspotSoftware/dutctl/commit/ff7cb6f4efe6ef586ab1d5a45d50f22fa66fbfab))


### Documentation

* add BSD-2 license text to non autogenerated go files ([a7748ed](https://github.com/BlindspotSoftware/dutctl/commit/a7748ed6ac749c5b73524266f65dc7ee3efb9492))
* fix typo in readme.md ([2e7d227](https://github.com/BlindspotSoftware/dutctl/commit/2e7d22797e04fc6376254e88584e065f1a956130))
* improve doc comments of the module interface ([39a7e7b](https://github.com/BlindspotSoftware/dutctl/commit/39a7e7b8d89d82400277b67368f5efda2b5133c9))
* improve usage message in dutctl ([eede31d](https://github.com/BlindspotSoftware/dutctl/commit/eede31ddcde939cde64cd45722cf4efbba460848))


### Other Work

* cover pkg/dut.Devlist ([992eac5](https://github.com/BlindspotSoftware/dutctl/commit/992eac50d323bb4a68155eae477a63663ba6e8fd))
* group module plug-in imports for dutagent ([f13ed70](https://github.com/BlindspotSoftware/dutctl/commit/f13ed70350ea43ff5eeb99c0875461e3b8b479b5))
* improve dutctl output format ([03208e5](https://github.com/BlindspotSoftware/dutctl/commit/03208e58dce96f084ef7f433a6716734dc874875))
* improve error handling on unregistered modules ([d7890d8](https://github.com/BlindspotSoftware/dutctl/commit/d7890d89260a0e5d716f1f354709262faeacf870))
* improve error messages in dutctl ([5a0130f](https://github.com/BlindspotSoftware/dutctl/commit/5a0130fad402f6e7248b94a7b06d2f4597869b42))
* improve queries to dut.Devlist ([be3345b](https://github.com/BlindspotSoftware/dutctl/commit/be3345b824b7ece558670a0976b5adddbbe6b07c))

## 0.1.0 (2024-09-26)


### Features

* add generated code for RPC service ([f3770a0](https://github.com/BlindspotSoftware/dutctl/commit/f3770a0199cbbfce477192c33995d659f0e9d562))
* add pkg/dut ([51027cc](https://github.com/BlindspotSoftware/dutctl/commit/51027ccb5d5471462afead94349a259a0cef72b9))
* add pkg/module with Module interface definition ([594224c](https://github.com/BlindspotSoftware/dutctl/commit/594224ce621fb2145f523975ccd47f29df3143ef))
* cli for dutagent ([1388faf](https://github.com/BlindspotSoftware/dutctl/commit/1388faf88d96cff39f3bb90caec0d9c505675b69))
* cli for dutctl ([80fc4bc](https://github.com/BlindspotSoftware/dutctl/commit/80fc4bc4022ab99a3338b5e0d8dde1bbfe5c8ceb))
* communication design: add 'Details'-RPC ([5c41ffa](https://github.com/BlindspotSoftware/dutctl/commit/5c41ffa32a927ef107d9f2080a38d4f1f2c0a879))
* draft command for dutctl ([8eed5d1](https://github.com/BlindspotSoftware/dutctl/commit/8eed5d176baf860abddc08b135c62cab20c4b545))
* draft commands for dutctl and dutagend ([5c80434](https://github.com/BlindspotSoftware/dutctl/commit/5c80434483ed619902b594793594af7adc6d219f))
* dutagent runs module de-init on clean-up ([64ecf79](https://github.com/BlindspotSoftware/dutctl/commit/64ecf79af24740c0e87ea04c12c86b6663786192))
* dutagent runs module init on startup ([c34757f](https://github.com/BlindspotSoftware/dutctl/commit/c34757f417935d145c5b4316c1d4cbefa8e87af8))
* dutctl: validate transmitted file names against cmdline content ([842ca42](https://github.com/BlindspotSoftware/dutctl/commit/842ca427dc7acf171e9f9e4c57da4561653e7044))
* file transfer between client and agent ([9b09125](https://github.com/BlindspotSoftware/dutctl/commit/9b0912557c09bff9e6aa9c63423aca0c746ec34c))
* multi-module support ([f3b5bb3](https://github.com/BlindspotSoftware/dutctl/commit/f3b5bb31679fcea294fc7b062c5b0c498b16987e))
* parse devices from YAML config ([62bad03](https://github.com/BlindspotSoftware/dutctl/commit/62bad0364c7f864aaf64bba51f902285dfc89b24))
* plug-in system for modules ([07f6338](https://github.com/BlindspotSoftware/dutctl/commit/07f6338c8daf5fac55fb9fe2ebcb9cfa94cfc663))
* protobuf spec ([21a406a](https://github.com/BlindspotSoftware/dutctl/commit/21a406ae0cfd0598a848d167e3b0ceadb36b8cdc))


### Bug Fixes

* chanio constructors return errors ([2737ff9](https://github.com/BlindspotSoftware/dutctl/commit/2737ff93f36ca1ea4a15f9ee8c40465eb3e72f88))
* dutagent example config: remove empty command ([d2a1f9c](https://github.com/BlindspotSoftware/dutctl/commit/d2a1f9ca20d5af8ce4a8925f8e8a8e4c740e89d3))
* dutagent module broker errors on bad file transfers ([8ca9292](https://github.com/BlindspotSoftware/dutctl/commit/8ca92925e0b9617cf0e95186cc87fcf5ac3730dd))
* synch terminiation of module execution ([83d6f65](https://github.com/BlindspotSoftware/dutctl/commit/83d6f6529f0ee673727581f0ed4d14cdbd5450ec))


### Documentation

* add flow charts for RPCs ([d624218](https://github.com/BlindspotSoftware/dutctl/commit/d6242183204273d06e150bc7fe55064f4f3bbf14))
* add golangci-lint to CONTRIBUTING.md ([2ffe20b](https://github.com/BlindspotSoftware/dutctl/commit/2ffe20bae7aafec751a12058c12e04cebe02f770))
* add NLNet logo to README.md ([9d0ce00](https://github.com/BlindspotSoftware/dutctl/commit/9d0ce006a4461fad434c0d3fb4861c16ed01e447))
* communication design ([33f2611](https://github.com/BlindspotSoftware/dutctl/commit/33f26114bd2edb0e641cf2f34825667a4f18e60d))
* finalize initial project documentation ([e90b05d](https://github.com/BlindspotSoftware/dutctl/commit/e90b05d7245ddb98ad41cbab2fac3f0d0d6c3c93))
* finalize module documentation ([d182e1f](https://github.com/BlindspotSoftware/dutctl/commit/d182e1fa84a44368cfc79e607a6937a7db0e3bfa))
* fine tune module guide ([db2c43e](https://github.com/BlindspotSoftware/dutctl/commit/db2c43ef97c4e006a5f761892c9c89f3400f662c))
* fix code block style ([f171a81](https://github.com/BlindspotSoftware/dutctl/commit/f171a81d782ff1e15a23f3428145d60aac05707a))
* fix reference in protobuf/README.md ([6cecfb2](https://github.com/BlindspotSoftware/dutctl/commit/6cecfb204258225898297d4695e7d22c3d53b14d))
* installation instructions in protobuf/README.md ([f338748](https://github.com/BlindspotSoftware/dutctl/commit/f338748f1ef0ae25c56da072a48e976e607aed56))
* update Contributing.md regarding conventional commits ([6d1f593](https://github.com/BlindspotSoftware/dutctl/commit/6d1f59345cd1927a0f7e56edbcd300e776b4ad13))
* update module docu ([c464e7b](https://github.com/BlindspotSoftware/dutctl/commit/c464e7b8b30d3a893c0c4977e0099d4589d9961a))
* update README ([c64effa](https://github.com/BlindspotSoftware/dutctl/commit/c64effa173aaa9a634b186f8f8a938b407504c88))


### Other Work

* adapt naming in dutctl.proto ([bad6d7e](https://github.com/BlindspotSoftware/dutctl/commit/bad6d7ec94ff3b023856fd174287b6563d314a52))
* add device list type to pkg/dut ([c2a6f40](https://github.com/BlindspotSoftware/dutctl/commit/c2a6f4021822de9e7b0424c68c4b5a997edab433))
* code style and linting ([69f623f](https://github.com/BlindspotSoftware/dutctl/commit/69f623f108d75ae231a56ca01a96090a34d10049))
* dutagent cli ([69c7d4e](https://github.com/BlindspotSoftware/dutctl/commit/69c7d4e129c1f78c09151cde9c6d4318f6811cc7))
* dutagent-to-module communication ([6b41105](https://github.com/BlindspotSoftware/dutctl/commit/6b4110525cb802d697fb60a1efafa49aa73067b8))
* dutagent's Run RPC handler being a state machine ([97e5343](https://github.com/BlindspotSoftware/dutctl/commit/97e5343206a359dbbd79970a45f74cfecd7bf680))


### Chore

* release 0.1.0 ([96f8ad7](https://github.com/BlindspotSoftware/dutctl/commit/96f8ad7e92b252f4ba5a567ea0fc7bad0ce3e7a4))

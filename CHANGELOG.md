# Changelog

## [0.1.0](https://github.com/BlindspotSoftware/dutctl/compare/v0.1.0...v0.1.0) (2024-09-25)


### Features

* add generated code for RPC service ([f3770a0](https://github.com/BlindspotSoftware/dutctl/commit/f3770a0199cbbfce477192c33995d659f0e9d562))
* add pkg/dut ([51027cc](https://github.com/BlindspotSoftware/dutctl/commit/51027ccb5d5471462afead94349a259a0cef72b9))
* add pkg/module with Module interface definition ([594224c](https://github.com/BlindspotSoftware/dutctl/commit/594224ce621fb2145f523975ccd47f29df3143ef))
* cli for dutagent ([655b41f](https://github.com/BlindspotSoftware/dutctl/commit/655b41f7efce3081c008ebb9b8df07468438e530))
* cli for dutctl ([18644ba](https://github.com/BlindspotSoftware/dutctl/commit/18644ba56cb9676a05cdb3beba638628f52f064b))
* communication design: add 'Details'-RPC ([b721bb2](https://github.com/BlindspotSoftware/dutctl/commit/b721bb21743734fe4632dad7cf89580e330c242e))
* draft command for dutctl ([8eed5d1](https://github.com/BlindspotSoftware/dutctl/commit/8eed5d176baf860abddc08b135c62cab20c4b545))
* draft commands for dutctl and dutagend ([5c80434](https://github.com/BlindspotSoftware/dutctl/commit/5c80434483ed619902b594793594af7adc6d219f))
* dutagent runs module de-init on clean-up ([1517141](https://github.com/BlindspotSoftware/dutctl/commit/15171412468dbce16ef9c9ab191f8d3bf5c2657b))
* dutagent runs module init on startup ([705bd56](https://github.com/BlindspotSoftware/dutctl/commit/705bd56708ef0ff74ada10d68b289c89ba46a20b))
* dutctl: validate transmitted file names against cmdline content ([3bbe412](https://github.com/BlindspotSoftware/dutctl/commit/3bbe4124b0e82088bfa9840455a15706b8c7071d))
* file transfer between client and agent ([9b09125](https://github.com/BlindspotSoftware/dutctl/commit/9b0912557c09bff9e6aa9c63423aca0c746ec34c))
* multi-module support ([f3b5bb3](https://github.com/BlindspotSoftware/dutctl/commit/f3b5bb31679fcea294fc7b062c5b0c498b16987e))
* parse devices from YAML config ([62bad03](https://github.com/BlindspotSoftware/dutctl/commit/62bad0364c7f864aaf64bba51f902285dfc89b24))
* plug-in system for modules ([07f6338](https://github.com/BlindspotSoftware/dutctl/commit/07f6338c8daf5fac55fb9fe2ebcb9cfa94cfc663))
* protobuf spec ([21a406a](https://github.com/BlindspotSoftware/dutctl/commit/21a406ae0cfd0598a848d167e3b0ceadb36b8cdc))


### Bug Fixes

* chanio constructors return errors ([2737ff9](https://github.com/BlindspotSoftware/dutctl/commit/2737ff93f36ca1ea4a15f9ee8c40465eb3e72f88))
* dutagent example config: remove empty command ([55f8da5](https://github.com/BlindspotSoftware/dutctl/commit/55f8da5e3d6d63ed9e9c32d8b551ef1b93ae4a70))
* dutagent module broker errors on bad file transfers ([1a114ab](https://github.com/BlindspotSoftware/dutctl/commit/1a114abb9017be71ff4fae0aafeef52f9d700f6b))
* synch terminiation of module execution ([ed82f7e](https://github.com/BlindspotSoftware/dutctl/commit/ed82f7e80e181042c595236f5575eef8a4cda50b))


### Documentation

* add flow charts for RPCs ([d624218](https://github.com/BlindspotSoftware/dutctl/commit/d6242183204273d06e150bc7fe55064f4f3bbf14))
* add NLNet logo to README.md ([9d0ce00](https://github.com/BlindspotSoftware/dutctl/commit/9d0ce006a4461fad434c0d3fb4861c16ed01e447))
* communication design ([33f2611](https://github.com/BlindspotSoftware/dutctl/commit/33f26114bd2edb0e641cf2f34825667a4f18e60d))
* finalize initial project documentation ([e90b05d](https://github.com/BlindspotSoftware/dutctl/commit/e90b05d7245ddb98ad41cbab2fac3f0d0d6c3c93))
* finalize module documentation ([d182e1f](https://github.com/BlindspotSoftware/dutctl/commit/d182e1fa84a44368cfc79e607a6937a7db0e3bfa))
* fine tune module guide ([db2c43e](https://github.com/BlindspotSoftware/dutctl/commit/db2c43ef97c4e006a5f761892c9c89f3400f662c))
* fix code block style ([f171a81](https://github.com/BlindspotSoftware/dutctl/commit/f171a81d782ff1e15a23f3428145d60aac05707a))
* fix reference in protobuf/README.md ([6b6b768](https://github.com/BlindspotSoftware/dutctl/commit/6b6b7684f9c92997af151777dfc482463a62e45a))
* installation instructions in protobuf/README.md ([f2c9bbd](https://github.com/BlindspotSoftware/dutctl/commit/f2c9bbd281ad5162a1335467bdf89ec84845db68))
* update module docu ([c464e7b](https://github.com/BlindspotSoftware/dutctl/commit/c464e7b8b30d3a893c0c4977e0099d4589d9961a))


### Other Work

* adapt naming in dutctl.proto ([28566ba](https://github.com/BlindspotSoftware/dutctl/commit/28566ba32a91d5e2aa07c7c463b26ed0fa945594))
* add device list type to pkg/dut ([2d9ec29](https://github.com/BlindspotSoftware/dutctl/commit/2d9ec29694e46b056f77af99301d19788af71c7f))
* code style and linting ([69f623f](https://github.com/BlindspotSoftware/dutctl/commit/69f623f108d75ae231a56ca01a96090a34d10049))
* dutagent cli ([dc11693](https://github.com/BlindspotSoftware/dutctl/commit/dc116933ff0b53280d626eb99863e0bfe554c353))
* dutagent-to-module communication ([6b41105](https://github.com/BlindspotSoftware/dutctl/commit/6b4110525cb802d697fb60a1efafa49aa73067b8))
* dutagent's Run RPC handler being a state machine ([97e5343](https://github.com/BlindspotSoftware/dutctl/commit/97e5343206a359dbbd79970a45f74cfecd7bf680))


### CI

* update .release-please-manifest.json ([fb3f3a6](https://github.com/BlindspotSoftware/dutctl/commit/fb3f3a64db860091d7d6ec05175ae0b09e671312))
* update .release-please-manifest.json ([349817b](https://github.com/BlindspotSoftware/dutctl/commit/349817bc3cbe1ef02944abe3a21d612b07b3b014))

## 0.1.0 (2024-09-24)


### Features

* add generated code for RPC service ([f3770a0](https://github.com/BlindspotSoftware/dutctl/commit/f3770a0199cbbfce477192c33995d659f0e9d562))
* add pkg/dut ([51027cc](https://github.com/BlindspotSoftware/dutctl/commit/51027ccb5d5471462afead94349a259a0cef72b9))
* add pkg/module with Module interface definition ([594224c](https://github.com/BlindspotSoftware/dutctl/commit/594224ce621fb2145f523975ccd47f29df3143ef))
* cli for dutagent ([655b41f](https://github.com/BlindspotSoftware/dutctl/commit/655b41f7efce3081c008ebb9b8df07468438e530))
* cli for dutctl ([18644ba](https://github.com/BlindspotSoftware/dutctl/commit/18644ba56cb9676a05cdb3beba638628f52f064b))
* communication design: add 'Details'-RPC ([b721bb2](https://github.com/BlindspotSoftware/dutctl/commit/b721bb21743734fe4632dad7cf89580e330c242e))
* draft command for dutctl ([8eed5d1](https://github.com/BlindspotSoftware/dutctl/commit/8eed5d176baf860abddc08b135c62cab20c4b545))
* draft commands for dutctl and dutagend ([5c80434](https://github.com/BlindspotSoftware/dutctl/commit/5c80434483ed619902b594793594af7adc6d219f))
* dutagent runs module de-init on clean-up ([1517141](https://github.com/BlindspotSoftware/dutctl/commit/15171412468dbce16ef9c9ab191f8d3bf5c2657b))
* dutagent runs module init on startup ([705bd56](https://github.com/BlindspotSoftware/dutctl/commit/705bd56708ef0ff74ada10d68b289c89ba46a20b))
* dutctl: validate transmitted file names against cmdline content ([3bbe412](https://github.com/BlindspotSoftware/dutctl/commit/3bbe4124b0e82088bfa9840455a15706b8c7071d))
* file transfer between client and agent ([9b09125](https://github.com/BlindspotSoftware/dutctl/commit/9b0912557c09bff9e6aa9c63423aca0c746ec34c))
* multi-module support ([f3b5bb3](https://github.com/BlindspotSoftware/dutctl/commit/f3b5bb31679fcea294fc7b062c5b0c498b16987e))
* parse devices from YAML config ([62bad03](https://github.com/BlindspotSoftware/dutctl/commit/62bad0364c7f864aaf64bba51f902285dfc89b24))
* plug-in system for modules ([07f6338](https://github.com/BlindspotSoftware/dutctl/commit/07f6338c8daf5fac55fb9fe2ebcb9cfa94cfc663))
* protobuf spec ([21a406a](https://github.com/BlindspotSoftware/dutctl/commit/21a406ae0cfd0598a848d167e3b0ceadb36b8cdc))


### Bug Fixes

* chanio constructors return errors ([2737ff9](https://github.com/BlindspotSoftware/dutctl/commit/2737ff93f36ca1ea4a15f9ee8c40465eb3e72f88))
* dutagent example config: remove empty command ([55f8da5](https://github.com/BlindspotSoftware/dutctl/commit/55f8da5e3d6d63ed9e9c32d8b551ef1b93ae4a70))
* dutagent module broker errors on bad file transfers ([1a114ab](https://github.com/BlindspotSoftware/dutctl/commit/1a114abb9017be71ff4fae0aafeef52f9d700f6b))
* synch terminiation of module execution ([ed82f7e](https://github.com/BlindspotSoftware/dutctl/commit/ed82f7e80e181042c595236f5575eef8a4cda50b))


### Documentation

* add flow charts for RPCs ([d624218](https://github.com/BlindspotSoftware/dutctl/commit/d6242183204273d06e150bc7fe55064f4f3bbf14))
* add NLNet logo to README.md ([9d0ce00](https://github.com/BlindspotSoftware/dutctl/commit/9d0ce006a4461fad434c0d3fb4861c16ed01e447))
* communication design ([33f2611](https://github.com/BlindspotSoftware/dutctl/commit/33f26114bd2edb0e641cf2f34825667a4f18e60d))
* finalize initial project documentation ([e90b05d](https://github.com/BlindspotSoftware/dutctl/commit/e90b05d7245ddb98ad41cbab2fac3f0d0d6c3c93))
* finalize module documentation ([d182e1f](https://github.com/BlindspotSoftware/dutctl/commit/d182e1fa84a44368cfc79e607a6937a7db0e3bfa))
* fine tune module guide ([db2c43e](https://github.com/BlindspotSoftware/dutctl/commit/db2c43ef97c4e006a5f761892c9c89f3400f662c))
* fix code block style ([f171a81](https://github.com/BlindspotSoftware/dutctl/commit/f171a81d782ff1e15a23f3428145d60aac05707a))
* fix reference in protobuf/README.md ([6b6b768](https://github.com/BlindspotSoftware/dutctl/commit/6b6b7684f9c92997af151777dfc482463a62e45a))
* installation instructions in protobuf/README.md ([f2c9bbd](https://github.com/BlindspotSoftware/dutctl/commit/f2c9bbd281ad5162a1335467bdf89ec84845db68))
* update module docu ([c464e7b](https://github.com/BlindspotSoftware/dutctl/commit/c464e7b8b30d3a893c0c4977e0099d4589d9961a))


### Other Work

* adapt naming in dutctl.proto ([28566ba](https://github.com/BlindspotSoftware/dutctl/commit/28566ba32a91d5e2aa07c7c463b26ed0fa945594))
* add device list type to pkg/dut ([2d9ec29](https://github.com/BlindspotSoftware/dutctl/commit/2d9ec29694e46b056f77af99301d19788af71c7f))
* code style and linting ([69f623f](https://github.com/BlindspotSoftware/dutctl/commit/69f623f108d75ae231a56ca01a96090a34d10049))
* dutagent cli ([dc11693](https://github.com/BlindspotSoftware/dutctl/commit/dc116933ff0b53280d626eb99863e0bfe554c353))
* dutagent-to-module communication ([6b41105](https://github.com/BlindspotSoftware/dutctl/commit/6b4110525cb802d697fb60a1efafa49aa73067b8))
* dutagent's Run RPC handler being a state machine ([97e5343](https://github.com/BlindspotSoftware/dutctl/commit/97e5343206a359dbbd79970a45f74cfecd7bf680))


### CI

* update .release-please-manifest.json ([fb3f3a6](https://github.com/BlindspotSoftware/dutctl/commit/fb3f3a64db860091d7d6ec05175ae0b09e671312))
* update .release-please-manifest.json ([349817b](https://github.com/BlindspotSoftware/dutctl/commit/349817bc3cbe1ef02944abe3a21d612b07b3b014))

# [0.22.0](https://github.com/modulestf/facts/compare/v0.21.13...v0.22.0) (2026-11-27)


### Features

* **behaviour:** Name a review tool limit as the cause of an unverified trigger ([#156](https://github.com/modulestf/facts/issues/156)) ([471f199](https://github.com/modulestf/facts/commit/471f1999d668bcf2aa742bab49f7ecc8dd53bd0c))



## [0.21.13](https://github.com/modulestf/facts/compare/v0.21.12...v0.21.13) (2026-11-27)


### Bug Fixes

* **behaviour:** Report an added example directory no configuration names as one trigger per file ([#154](https://github.com/modulestf/facts/issues/154)) ([1abd644](https://github.com/modulestf/facts/commit/1abd6448882ea637b6f1fc532125b3acb6d8d4db))



## [0.21.12](https://github.com/modulestf/facts/compare/v0.21.11...v0.21.12) (2026-11-27)


### Bug Fixes

* Fail closed on repeated moved sources, duplicate variables and nested blocks moved between parents ([#155](https://github.com/modulestf/facts/issues/155)) ([020c2fd](https://github.com/modulestf/facts/commit/020c2fd58605582a361d4c4d5d7a5d725c26af56)), closes [#144](https://github.com/modulestf/facts/issues/144)



## [0.21.11](https://github.com/modulestf/facts/compare/v0.21.10...v0.21.11) (2026-11-27)


### Bug Fixes

* Treat .tofu files as unread configuration, and bound the held literals ([#152](https://github.com/modulestf/facts/issues/152)) ([1e2e8a4](https://github.com/modulestf/facts/commit/1e2e8a493680aa557071b0e6b53910565e3947ad)), closes [#150](https://github.com/modulestf/facts/issues/150)



## [0.21.10](https://github.com/modulestf/facts/compare/v0.21.9...v0.21.10) (2026-11-27)


### Bug Fixes

* Do not read an IAM path that only reaches IAM paths, or a string a function removes, as a file read ([#149](https://github.com/modulestf/facts/issues/149)) ([586693c](https://github.com/modulestf/facts/commit/586693c51832b15950034eeb8bc0d243020f45a8)), closes [#148](https://github.com/modulestf/facts/issues/148)



## [0.21.9](https://github.com/modulestf/facts/compare/v0.21.8...v0.21.9) (2026-11-27)


### Bug Fixes

* Refuse files-list shapes GitHub never sends ([#134](https://github.com/modulestf/facts/issues/134)) ([88a5f5e](https://github.com/modulestf/facts/commit/88a5f5e5060d77d4a65ea80657f9aea88573b46d)), closes [#130](https://github.com/modulestf/facts/issues/130)



## [0.21.8](https://github.com/modulestf/facts/compare/v0.21.7...v0.21.8) (2026-11-27)


### Bug Fixes

* Fail closed on a block or local declared twice in the compat maps ([#140](https://github.com/modulestf/facts/issues/140)) ([f0342dc](https://github.com/modulestf/facts/commit/f0342dc41939edca84b73047922b331d49ab9a36)), closes [#139](https://github.com/modulestf/facts/issues/139) [#138](https://github.com/modulestf/facts/issues/138) [#144](https://github.com/modulestf/facts/issues/144)



## [0.21.7](https://github.com/modulestf/facts/compare/v0.21.6...v0.21.7) (2026-11-27)


### Bug Fixes

* Read a variable's type as a type constraint in iam-policy-docs keys ([#142](https://github.com/modulestf/facts/issues/142)) ([7b8b431](https://github.com/modulestf/facts/commit/7b8b431edb6985cf1f20563958c5bd66ccf4ef81)), closes [#141](https://github.com/modulestf/facts/issues/141)



## [0.21.6](https://github.com/modulestf/facts/compare/v0.21.5...v0.21.6) (2026-11-27)


### Bug Fixes

* Fail closed on a module label declared twice ([#138](https://github.com/modulestf/facts/issues/138)) ([a65489d](https://github.com/modulestf/facts/commit/a65489d3ebaca4164111feae68b191be40e9e634)), closes [#136](https://github.com/modulestf/facts/issues/136)



## [0.21.5](https://github.com/modulestf/facts/compare/v0.21.4...v0.21.5) (2026-11-27)


### Bug Fixes

* Count every argument of a module call whose source changes in place ([#135](https://github.com/modulestf/facts/issues/135)) ([8d17cd4](https://github.com/modulestf/facts/commit/8d17cd4b47c79694ea5183bca81033255b31d75f)), closes [#132](https://github.com/modulestf/facts/issues/132)



## [0.21.4](https://github.com/modulestf/facts/compare/v0.21.3...v0.21.4) (2026-11-27)


### Bug Fixes

* Fail closed on moved chains, cross-type and malformed moved addresses ([#128](https://github.com/modulestf/facts/issues/128)) ([af7312c](https://github.com/modulestf/facts/commit/af7312cc4685af422f5a7ebb63ed139f344e5b6c)), closes [#66](https://github.com/modulestf/facts/issues/66) [#66](https://github.com/modulestf/facts/issues/66)



## [0.21.3](https://github.com/modulestf/facts/compare/v0.21.2...v0.21.3) (2026-11-27)


### Bug Fixes

* Never read a removed file with no patch as inert ([#127](https://github.com/modulestf/facts/issues/127)) ([c995f81](https://github.com/modulestf/facts/commit/c995f81eebd6f299eb1f3e5dfd4bfc23cf5d091c)), closes [#23](https://github.com/modulestf/facts/issues/23)



## [0.21.2](https://github.com/modulestf/facts/compare/v0.21.1...v0.21.2) (2026-11-27)


### Bug Fixes

* Refuse a quoted dynamic iterator in iam-policy-docs ([#125](https://github.com/modulestf/facts/issues/125)) ([76e4b70](https://github.com/modulestf/facts/commit/76e4b70f06f20a6f5d25a808d1b832fe1f4aa7b2)), closes [#54](https://github.com/modulestf/facts/issues/54) [#54](https://github.com/modulestf/facts/issues/54)



## [0.21.1](https://github.com/modulestf/facts/compare/v0.21.0...v0.21.1) (2026-11-27)


### Bug Fixes

* Raise default-changed only for a new input that existing configuration reads ([#66](https://github.com/modulestf/facts/issues/66)) ([1f2ffa7](https://github.com/modulestf/facts/commit/1f2ffa75ef67f58633f28100d49d81cb989d21ed)), closes [#61](https://github.com/modulestf/facts/issues/61) [vpc#1305](https://github.com/vpc/issues/1305) [eks#3743](https://github.com/eks/issues/3743) [#61](https://github.com/modulestf/facts/issues/61) [vpc#1305](https://github.com/vpc/issues/1305) [eks#3743](https://github.com/eks/issues/3743) [vpc#1188](https://github.com/vpc/issues/1188) [vpc#1304](https://github.com/vpc/issues/1304) [eks#3746](https://github.com/eks/issues/3746) [eks#3769](https://github.com/eks/issues/3769) [vpc#1305](https://github.com/vpc/issues/1305) [eks#3743](https://github.com/eks/issues/3743) [vpc#1188](https://github.com/vpc/issues/1188) [vpc#1304](https://github.com/vpc/issues/1304) [eks#3746](https://github.com/eks/issues/3746) [eks#3769](https://github.com/eks/issues/3769) [vpc#1305](https://github.com/vpc/issues/1305)



# [0.21.0](https://github.com/modulestf/facts/compare/v0.20.7...v0.21.0) (2026-11-27)


### Features

* Report IAM policy documents and the resources that use them ([#54](https://github.com/modulestf/facts/issues/54)) ([8080007](https://github.com/modulestf/facts/commit/8080007346333fb93b9ca8575a6dbf3f31b69f80)), closes [#117](https://github.com/modulestf/facts/issues/117) [#118](https://github.com/modulestf/facts/issues/118)



## [0.20.7](https://github.com/modulestf/facts/compare/v0.20.6...v0.20.7) (2026-11-27)


### Bug Fixes

* Fail closed on module calls through links to submodule pointers ([#122](https://github.com/modulestf/facts/issues/122)) ([f38b922](https://github.com/modulestf/facts/commit/f38b9222f135c5a8f51012d2233269b7e40d7ffd)), closes [#116](https://github.com/modulestf/facts/issues/116) [#116](https://github.com/modulestf/facts/issues/116)



## [0.20.6](https://github.com/modulestf/facts/compare/v0.20.5...v0.20.6) (2026-11-27)


### Bug Fixes

* **affected:** Never read a submodule pointer change as inert ([#116](https://github.com/modulestf/facts/issues/116)) ([673c68b](https://github.com/modulestf/facts/commit/673c68b1287652b5bc3990fc5b8aecf171aac06b))



## [0.20.5](https://github.com/modulestf/facts/compare/v0.20.4...v0.20.5) (2026-11-26)


### Bug Fixes

* read a committed .terraform directory as repository content ([#118](https://github.com/modulestf/facts/issues/118)) ([42098ed](https://github.com/modulestf/facts/commit/42098ed88ef15b8ff37fbdfac57bacdae0c99cfa))



## [0.20.4](https://github.com/modulestf/facts/compare/v0.20.3...v0.20.4) (2026-11-26)


### Performance Improvements

* Bound the HCL tokens one run parses and lex a file once where no tokens are needed ([#117](https://github.com/modulestf/facts/issues/117)) ([4fde468](https://github.com/modulestf/facts/commit/4fde468765a23126c7398f5e5f1b995bb4baff13)), closes [terraform-aws-eks#3764](https://github.com/terraform-aws-eks/issues/3764) [terraform-aws-eks#3764](https://github.com/terraform-aws-eks/issues/3764) [terraform-aws-security-group#355](https://github.com/terraform-aws-security-group/issues/355) [terraform-aws-security-group#355](https://github.com/terraform-aws-security-group/issues/355) [security-group#355](https://github.com/security-group/issues/355) [terraform-aws-eks#3766](https://github.com/terraform-aws-eks/issues/3766) [#3764](https://github.com/modulestf/facts/issues/3764) [terraform-aws-security-group#362](https://github.com/terraform-aws-security-group/issues/362) [#355](https://github.com/modulestf/facts/issues/355)



## [0.20.3](https://github.com/modulestf/facts/compare/v0.20.2...v0.20.3) (2026-11-26)


### Bug Fixes

* Refuse a module file over the parse limits as a G2 hit, not a crash ([#113](https://github.com/modulestf/facts/issues/113)) ([81b60d0](https://github.com/modulestf/facts/commit/81b60d045d2cadb643157b4985a289df01d4fa2d))



## [0.20.2](https://github.com/modulestf/facts/compare/v0.20.1...v0.20.2) (2026-11-26)


### Bug Fixes

* raise the file size cap to 2 MiB and the token cap to 262144 ([#110](https://github.com/modulestf/facts/issues/110)) ([ad6807b](https://github.com/modulestf/facts/commit/ad6807b4698c3eff658522b67dd3273658ca95d7))



## [0.20.1](https://github.com/modulestf/facts/compare/v0.20.0...v0.20.1) (2026-11-26)


### Bug Fixes

* **behaviour:** Emit the data-read L1 trigger only when what decides knownness changes ([#111](https://github.com/modulestf/facts/issues/111)) ([e4be992](https://github.com/modulestf/facts/commit/e4be992142ba012eff6c6efbaa3cb8d8220efae8))



# [0.20.0](https://github.com/modulestf/facts/compare/v0.19.1...v0.20.0) (2026-11-26)


### Features

* Read module sources as Terraform does in plan-gates ([#107](https://github.com/modulestf/facts/issues/107)) ([b448f29](https://github.com/modulestf/facts/commit/b448f29d9bbd2e726f18ed42d5d9a6ea9c0826f1)), closes [#98](https://github.com/modulestf/facts/issues/98)



## [0.19.1](https://github.com/modulestf/facts/compare/v0.19.0...v0.19.1) (2026-11-26)


### Bug Fixes

* Quote the remaining untrusted names in error text ([#106](https://github.com/modulestf/facts/issues/106)) ([55af065](https://github.com/modulestf/facts/commit/55af065eae13b02e2e210ac3b97b58649944f2c8)), closes [#88](https://github.com/modulestf/facts/issues/88) [#96](https://github.com/modulestf/facts/issues/96)



# [0.19.0](https://github.com/modulestf/facts/compare/v0.18.0...v0.19.0) (2026-11-26)


### Features

* Spend the trigger cap on triggers that need evidence ([#70](https://github.com/modulestf/facts/issues/70)) ([2f3523c](https://github.com/modulestf/facts/commit/2f3523c9d18f570da56afdcdfc22ea9403cdba5f)), closes [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634) [rds#634](https://github.com/rds/issues/634) [rds#634](https://github.com/rds/issues/634) [rds#634](https://github.com/rds/issues/634) [rds#634](https://github.com/rds/issues/634) [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634) [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634)



# [0.18.0](https://github.com/modulestf/facts/compare/v0.17.0...v0.18.0) (2026-11-26)


### Features

* Add schemas for every JSON report and a JSON form of rules ([#55](https://github.com/modulestf/facts/issues/55)) ([39e4eab](https://github.com/modulestf/facts/commit/39e4eaba08486b77caa8727720e2e91fbe454f5a)), closes [#47](https://github.com/modulestf/facts/issues/47) [#47](https://github.com/modulestf/facts/issues/47) [modulestf/modulestf#230](https://github.com/modulestf/modulestf/issues/230)



# [0.17.0](https://github.com/modulestf/facts/compare/v0.16.0...v0.17.0) (2026-11-26)


### Features

* Add plan-gates, the plan pass gates G0 to G3 as an engine command ([#98](https://github.com/modulestf/facts/issues/98)) ([1a14c6a](https://github.com/modulestf/facts/commit/1a14c6af494a8bf56bf220f70693dc647a738cb9))



# [0.16.0](https://github.com/modulestf/facts/compare/v0.15.1...v0.16.0) (2026-11-26)


### Features

* Add fixture-build, which builds a ladder level's fixture from type constraints ([#83](https://github.com/modulestf/facts/issues/83)) ([a4cf038](https://github.com/modulestf/facts/commit/a4cf038dac39cd0206aa2d93199128d24f36bfd1))



## [0.15.1](https://github.com/modulestf/facts/compare/v0.15.0...v0.15.1) (2026-11-26)


### Bug Fixes

* Keep untrusted text from adding lines to error output ([#96](https://github.com/modulestf/facts/issues/96)) ([5cfff93](https://github.com/modulestf/facts/commit/5cfff93f31fdfad5010e459859ee085f60c28982))



# [0.15.0](https://github.com/modulestf/facts/compare/v0.14.4...v0.15.0) (2026-11-26)


### Features

* Read pre-commit-terraform wrapper and terraform-docs pins ([#91](https://github.com/modulestf/facts/issues/91)) ([2905df4](https://github.com/modulestf/facts/commit/2905df4a1ff308a852cd3cdb21a7a2b3faedaeac))



## [0.14.4](https://github.com/modulestf/facts/compare/v0.14.3...v0.14.4) (2026-11-26)


### Bug Fixes

* Build on 32-bit and illumos targets, quote provider sources, skip hidden directories ([#93](https://github.com/modulestf/facts/issues/93)) ([c4381f2](https://github.com/modulestf/facts/commit/c4381f21366ecb9d075f5b8b0893d6da13790684))



## [0.14.3](https://github.com/modulestf/facts/compare/v0.14.2...v0.14.3) (2026-11-26)


### Bug Fixes

* Read resource.<type>.<name> references as <type>.<name> ([#89](https://github.com/modulestf/facts/issues/89)) ([36b1d07](https://github.com/modulestf/facts/commit/36b1d075c9d9d6703fec23151833822e92babbe0))



## [0.14.2](https://github.com/modulestf/facts/compare/v0.14.1...v0.14.2) (2026-11-26)


### Bug Fixes

* Check and create the stats file through one parent directory descriptor ([#88](https://github.com/modulestf/facts/issues/88)) ([9509235](https://github.com/modulestf/facts/commit/9509235846f7a7f9d3714e54ae0369b21dbc2a8e))



## [0.14.1](https://github.com/modulestf/facts/compare/v0.14.0...v0.14.1) (2026-11-26)


### Bug Fixes

* Do not report an example with only a linked directory as clean ([#85](https://github.com/modulestf/facts/issues/85)) ([782e329](https://github.com/modulestf/facts/commit/782e329cfbe37ca95037056951da99547f72ac5b))



# [0.14.0](https://github.com/modulestf/facts/compare/v0.13.0...v0.14.0) (2026-11-26)


### Features

* Write per-phase timing and memory stats on request ([#62](https://github.com/modulestf/facts/issues/62)) ([b46c154](https://github.com/modulestf/facts/commit/b46c15484a92a4031c8c8bc4f57a815b2a614793)), closes [#73](https://github.com/modulestf/facts/issues/73) [#74](https://github.com/modulestf/facts/issues/74) [#74](https://github.com/modulestf/facts/issues/74) [#60](https://github.com/modulestf/facts/issues/60)



# [0.13.0](https://github.com/modulestf/facts/compare/v0.12.6...v0.13.0) (2026-11-26)


### Features

* Report changed attributes and their readers ([#60](https://github.com/modulestf/facts/issues/60)) ([64dc9e1](https://github.com/modulestf/facts/commit/64dc9e160853ecfef8f9cd3859b870bc1aca03f7))



## [0.12.6](https://github.com/modulestf/facts/compare/v0.12.5...v0.12.6) (2026-11-26)


### Bug Fixes

* Do not read an IAM path of "/" as a read of every file ([#75](https://github.com/modulestf/facts/issues/75)) ([0681818](https://github.com/modulestf/facts/commit/0681818d39db09143fd1471d6fda1f173fd4444f)), closes [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634) [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634) [terraform-aws-rds#634](https://github.com/terraform-aws-rds/issues/634)



## [0.12.5](https://github.com/modulestf/facts/compare/v0.12.4...v0.12.5) (2026-11-26)


### Bug Fixes

* Bound every command with a deadline and an output cap ([#74](https://github.com/modulestf/facts/issues/74)) ([b38ca60](https://github.com/modulestf/facts/commit/b38ca60bd55cf67704aa839d51e55071fd3fe041))



## [0.12.4](https://github.com/modulestf/facts/compare/v0.12.3...v0.12.4) (2026-11-26)


### Bug Fixes

* Lint a working copy that holds large build files ([#73](https://github.com/modulestf/facts/issues/73)) ([d8e7a6f](https://github.com/modulestf/facts/commit/d8e7a6feb7d325eb3fbe59130fdf9d568b764de3))



## [0.12.3](https://github.com/modulestf/facts/compare/v0.12.2...v0.12.3) (2026-11-26)


### Performance Improvements

* Decode only the provider schema types a module uses ([#67](https://github.com/modulestf/facts/issues/67)) ([f06e622](https://github.com/modulestf/facts/commit/f06e62225791a8931236d646c06fb3bace5b5ec3))



## [0.12.2](https://github.com/modulestf/facts/compare/v0.12.1...v0.12.2) (2026-11-26)


### Bug Fixes

* Raise fewer false candidates for policy and template changes ([#61](https://github.com/modulestf/facts/issues/61)) ([a6786cd](https://github.com/modulestf/facts/commit/a6786cd0e8978f30e969f67f2e2e15befd39d5d0)), closes [vpc#1320](https://github.com/vpc/issues/1320) [terraform-aws-iam#665](https://github.com/terraform-aws-iam/issues/665) [iam#665](https://github.com/iam/issues/665) [eks-pod-identity#75](https://github.com/eks-pod-identity/issues/75) [#62](https://github.com/modulestf/facts/issues/62) [iam#646](https://github.com/iam/issues/646) [#652](https://github.com/modulestf/facts/issues/652) [lambda#783](https://github.com/lambda/issues/783) [vpc#1320](https://github.com/vpc/issues/1320) [vpc#1321](https://github.com/vpc/issues/1321) [s3-bucket#397](https://github.com/s3-bucket/issues/397) [#408](https://github.com/modulestf/facts/issues/408) [eventbridge#207](https://github.com/eventbridge/issues/207) [#213](https://github.com/modulestf/facts/issues/213) [iam#654](https://github.com/iam/issues/654) [#671](https://github.com/modulestf/facts/issues/671) [notify-slack#273](https://github.com/notify-slack/issues/273) [eks#3743](https://github.com/eks/issues/3743) [#3758](https://github.com/modulestf/facts/issues/3758) [vpc#1190](https://github.com/vpc/issues/1190) [alb#436](https://github.com/alb/issues/436) [app-runner#21](https://github.com/app-runner/issues/21) [batch#52](https://github.com/batch/issues/52) [cloudwatch#85](https://github.com/cloudwatch/issues/85) [customer-gateway#26](https://github.com/customer-gateway/issues/26) [elasticache#77](https://github.com/elasticache/issues/77) [elb#51](https://github.com/elb/issues/51) [emr#52](https://github.com/emr/issues/52) [fsx#12](https://github.com/fsx/issues/12) [key-pair#36](https://github.com/key-pair/issues/36) [kms#45](https://github.com/kms/issues/45) [notify-slack#282](https://github.com/notify-slack/issues/282) [opensearch#59](https://github.com/opensearch/issues/59) [redshift#113](https://github.com/redshift/issues/113) [vpn-gateway#106](https://github.com/vpn-gateway/issues/106) [s3-bucket#414](https://github.com/s3-bucket/issues/414) [eks-pod-identity#75](https://github.com/eks-pod-identity/issues/75)



## [0.12.1](https://github.com/modulestf/facts/compare/v0.12.0...v0.12.1) (2026-11-26)


### Performance Improvements

* Lex each file once and speed up three slow rules ([#68](https://github.com/modulestf/facts/issues/68)) ([dbdc83d](https://github.com/modulestf/facts/commit/dbdc83d2ab9cf1eb905c0cc35dfe256a79298570)), closes [facts#62](https://github.com/facts/issues/62) [#62](https://github.com/modulestf/facts/issues/62) [#61](https://github.com/modulestf/facts/issues/61)



# [0.12.0](https://github.com/modulestf/facts/compare/v0.11.1...v0.12.0) (2026-11-26)


### Features

* Let affected select examples that call a module root ([#57](https://github.com/modulestf/facts/issues/57)) ([8f00a9e](https://github.com/modulestf/facts/commit/8f00a9e5b1a9914fe3fcdedee1c29436a9148e45))



## [0.11.1](https://github.com/modulestf/facts/compare/v0.11.0...v0.11.1) (2026-11-26)


### Bug Fixes

* Stop two tool rules firing on exempt or empty directories ([#65](https://github.com/modulestf/facts/issues/65)) ([a9528a2](https://github.com/modulestf/facts/commit/a9528a2fef7403c90be62d2f32ff737a5934e58a)), closes [lambda#778](https://github.com/lambda/issues/778) [#780](https://github.com/modulestf/facts/issues/780) [#781](https://github.com/modulestf/facts/issues/781) [terraform-aws-alb#440](https://github.com/terraform-aws-alb/issues/440) [#38](https://github.com/modulestf/facts/issues/38) [lambda#778](https://github.com/lambda/issues/778) [#780](https://github.com/modulestf/facts/issues/780) [#781](https://github.com/modulestf/facts/issues/781) [alb#440](https://github.com/alb/issues/440) [vpc#1188](https://github.com/vpc/issues/1188) [iam#671](https://github.com/iam/issues/671) [eks-pod-identity#68](https://github.com/eks-pod-identity/issues/68) [eventbridge#213](https://github.com/eventbridge/issues/213)



# [0.11.0](https://github.com/modulestf/facts/compare/v0.10.0...v0.11.0) (2026-11-26)


### Features

* Report variable declarations and schema requirement on behaviour triggers ([#64](https://github.com/modulestf/facts/issues/64)) ([5230652](https://github.com/modulestf/facts/commit/523065239075231b0e8dd7e2f6266a7c640611c3))



# [0.10.0](https://github.com/modulestf/facts/compare/v0.9.0...v0.10.0) (2026-11-25)


### Features

* Make the lint text report readable and skip base-only rules without a change ([#56](https://github.com/modulestf/facts/issues/56)) ([3382d8d](https://github.com/modulestf/facts/commit/3382d8d0112ba67a7ef7304dbdaa5b04f4f965d2))



# [0.9.0](https://github.com/modulestf/facts/compare/v0.8.0...v0.9.0) (2026-11-25)


### Features

* Report the version, report schema versions and features as JSON ([#47](https://github.com/modulestf/facts/issues/47)) ([6ac9dd4](https://github.com/modulestf/facts/commit/6ac9dd4b9c8300cd35128ec01b73468d62f1f0d0))



# [0.8.0](https://github.com/modulestf/facts/compare/v0.7.1...v0.8.0) (2026-11-15)


### Features

* **behaviour:** Let an added optional variable follow its use-site triggers ([#39](https://github.com/modulestf/facts/issues/39)) ([5f7b962](https://github.com/modulestf/facts/commit/5f7b962c1df8c947590589a96609b26905f95829))



## [0.7.1](https://github.com/modulestf/facts/compare/v0.7.0...v0.7.1) (2026-11-14)


### Bug Fixes

* skip profile.putin-khuylo-missing for a base root that never declared the input ([#38](https://github.com/modulestf/facts/issues/38)) ([c6dba22](https://github.com/modulestf/facts/commit/c6dba22c8346fd6df30472cdfc1e0b724d53b36f)), closes [modulestf/modulestf#150](https://github.com/modulestf/modulestf/issues/150)



# [0.7.0](https://github.com/modulestf/facts/compare/v0.6.2...v0.7.0) (2026-11-09)


### Features

* Gate profile.title-truncated under release: none ([#36](https://github.com/modulestf/facts/issues/36)) ([a03e864](https://github.com/modulestf/facts/commit/a03e864ed9e26af949aa0de9beea43bd13de0018)), closes [modulestf/modulestf#130](https://github.com/modulestf/modulestf/issues/130) [#130](https://github.com/modulestf/facts/issues/130)



## [0.6.2](https://github.com/modulestf/facts/compare/v0.6.1...v0.6.2) (2026-11-08)


### Bug Fixes

* classifier noise and policy false positives in nested dynamic blocks ([#34](https://github.com/modulestf/facts/issues/34)) ([f89b333](https://github.com/modulestf/facts/commit/f89b33334592e98a91a7d1b51f0f8097e8829439)), closes [terraform-aws-modules/terraform-aws-cloudfront#210](https://github.com/terraform-aws-modules/terraform-aws-cloudfront/issues/210) [terraform-aws-modules/terraform-aws-dynamodb-table#117](https://github.com/terraform-aws-modules/terraform-aws-dynamodb-table/issues/117)



## [0.6.1](https://github.com/modulestf/facts/compare/v0.6.0...v0.6.1) (2026-11-06)


### Bug Fixes

* check outputs of discovered module areas for structure.output-no-try ([#31](https://github.com/modulestf/facts/issues/31)) ([097e3d4](https://github.com/modulestf/facts/commit/097e3d411cde73f0901edfc537c4e807a8e00f80))



# [0.6.0](https://github.com/modulestf/facts/compare/v0.5.0...v0.6.0) (2026-11-06)


### Features

* module-area ownership, module callers and profile options ([#28](https://github.com/modulestf/facts/issues/28)) ([c2599c0](https://github.com/modulestf/facts/commit/c2599c0350d22e6cbe5f16ef6753e385c0c32129))



# [0.5.0](https://github.com/modulestf/facts/compare/v0.4.1...v0.5.0) (2026-11-05)


### Features

* **areas:** discover module directories outside the modules/examples layout ([#26](https://github.com/modulestf/facts/issues/26)) ([8be9ae2](https://github.com/modulestf/facts/commit/8be9ae242ce6d837c42bcb9c0ad6a47979b4a2e9))



## [0.4.1](https://github.com/modulestf/facts/compare/v0.4.0...v0.4.1) (2026-11-04)


### Bug Fixes

* **diff:** a removed .tf file with no patch and no line was empty ([#23](https://github.com/modulestf/facts/issues/23)) ([fdd49a5](https://github.com/modulestf/facts/commit/fdd49a5afe6543cd658457a5c8e681bf299aa35d))



# [0.4.0](https://github.com/modulestf/facts/compare/v0.4.0-rc.4...v0.4.0) (2026-11-04)

First stable release. It is the code of v0.4.0-rc.4, plus the build and release changes below.


### Features

* release binaries for macOS (`darwin_amd64`, `darwin_arm64`) and a Homebrew formula in [modulestf/homebrew-tap](https://github.com/modulestf/homebrew-tap)
* release notes come from this changelog, which the release workflow now writes

# [0.4.0-rc.4](https://github.com/modulestf/facts/compare/v0.4.0-rc.3...v0.4.0-rc.4) (2026-11-03)


### Features

* **behaviour:** null side for an added provider attribute; data sources at L1 only ([#20](https://github.com/modulestf/facts/issues/20)) ([8703676](https://github.com/modulestf/facts/commit/870367669293c59fa5249569e450249ca359cb48))

# [0.4.0-rc.3](https://github.com/modulestf/facts/compare/v0.4.0-rc.2...v0.4.0-rc.3) (2026-11-02)


### Bug Fixes

* no null states for a managed resource id in the L1 sweep ([#17](https://github.com/modulestf/facts/issues/17)) ([4a56b4c](https://github.com/modulestf/facts/commit/4a56b4c718971c46b1e1028dfce039cc328b79e9))


### Features

* **behaviour:** completeness, outstanding differences and evidence digest; merged triggers; predates_change ([#16](https://github.com/modulestf/facts/issues/16)) ([0c24a80](https://github.com/modulestf/facts/commit/0c24a8061fc75bedd8f237052c9a741b5bab7d26))
* **depth:** decide the review depth of a pull request ([#19](https://github.com/modulestf/facts/issues/19)) ([77c848b](https://github.com/modulestf/facts/commit/77c848bd87ff4211cf786d1dd8cfd842a1e902dd))

# [0.4.0-rc.2](https://github.com/modulestf/facts/compare/v0.4.0-rc.1...v0.4.0-rc.2) (2026-10-31)


### Bug Fixes

* **behaviour:** report the levels that ran and explain the L3 length of a value that turns unknown ([#15](https://github.com/modulestf/facts/issues/15)) ([547ce2a](https://github.com/modulestf/facts/commit/547ce2a0a25c90ddce5daf16ee6dced7bb478b43))

# [0.4.0-rc.1](https://github.com/modulestf/facts/compare/v0.3.0-rc.2...v0.4.0-rc.1) (2026-10-31)


### Bug Fixes

* a file the diff does not list is unchanged, even when oversize ([#9](https://github.com/modulestf/facts/issues/9)) ([5cfa190](https://github.com/modulestf/facts/commit/5cfa19060178380818602ef721e4dfb236a0d89b))


### Features

* affected lists the examples a change can affect ([#8](https://github.com/modulestf/facts/issues/8)) ([7ae99fb](https://github.com/modulestf/facts/commit/7ae99fb6c78640326fc0680dc8a701b5a810f9ad))
* behaviour and claims-eval classify a change and judge the evidence for it ([#12](https://github.com/modulestf/facts/issues/12)) ([d68b12a](https://github.com/modulestf/facts/commit/d68b12ac113418f98034b78160b54adfb8ea28a5))
* behaviour routes count, for_each and dynamic changes to L2 and stronger case sets ([#13](https://github.com/modulestf/facts/issues/13)) ([84d1cc3](https://github.com/modulestf/facts/commit/84d1cc3f650967bcb9e289a14b6ddfb9a6754537))
* **cli:** move mtf-facts to cobra with grouped help, and organize the README around usage ([#61](https://github.com/modulestf/facts/issues/61)) ([5578eaf](https://github.com/modulestf/facts/commit/5578eaf639db590821aaa7ccf5b8a48aefeea53c))
* fixture-check gates the scratch fixtures a review runs ([#10](https://github.com/modulestf/facts/issues/10)) ([1c6f2ed](https://github.com/modulestf/facts/commit/1c6f2edc08b57aaf80f24e2b193d01f35bb213bc))
* fixture-check L2 gates plan-only terraform tests with mock providers ([#11](https://github.com/modulestf/facts/issues/11)) ([e01117f](https://github.com/modulestf/facts/commit/e01117fb275a7432ea73ed1b0eb9db658abb55de))
* lint --diff and --full-tree, callers and scope in the report ([#6](https://github.com/modulestf/facts/issues/6)) ([ed664b3](https://github.com/modulestf/facts/commit/ed664b33d782c24e6d9e6c0f76fb98c2fcdd2462))
* lint --json --ai writes the smaller report a model reads ([#7](https://github.com/modulestf/facts/issues/7)) ([765e4b8](https://github.com/modulestf/facts/commit/765e4b8a08f01cab3bd254601e78f07c4b0a123b))

# [0.3.0-rc.2](https://github.com/modulestf/facts/compare/v0.3.0-rc.1...v0.3.0-rc.2) (2026-10-24)


### Bug Fixes

* shrink candidate evidence of attribute-not-output and provider-floor-raised ([#56](https://github.com/modulestf/facts/issues/56)) ([dacaf9e](https://github.com/modulestf/facts/commit/dacaf9e13311c574d18109f7d61311fbfdae3e56))


### Features

* **merge:** read an optional counts object in the model reply ([#57](https://github.com/modulestf/facts/issues/57)) ([356f78e](https://github.com/modulestf/facts/commit/356f78e9904da435a36d4ce8db7b0f013a8f1354))

# [0.3.0-rc.1](https://github.com/modulestf/facts/compare/v0.2.0-rc.2...v0.3.0-rc.1) (2026-10-24)


### Bug Fixes

* refuse a short separator row and require the base for scope rules ([#54](https://github.com/modulestf/facts/issues/54)) ([e411849](https://github.com/modulestf/facts/commit/e4118494c130b0a15d994aacac34bdd0a6ab5ebe))

# [0.2.0-rc.2](https://github.com/modulestf/facts/compare/v0.2.0-rc.1...v0.2.0-rc.2) (2026-10-24)


### Bug Fixes

* attribute-not-output reads nested computed attributes ([#43](https://github.com/modulestf/facts/issues/43)) ([c009a76](https://github.com/modulestf/facts/commit/c009a761899434b05bacea12a5c2df4ca5706053))
* make tests/schemas-test.sh deterministic and run it in CI ([#50](https://github.com/modulestf/facts/issues/50)) ([e1565d6](https://github.com/modulestf/facts/commit/e1565d67ce2ad0001d3fd9e0573f3068f5789bf6))
* name the raw schema file in the missing-type reason ([#49](https://github.com/modulestf/facts/issues/49)) ([44a9bfa](https://github.com/modulestf/facts/commit/44a9bfa73a94ef75b4f8e32887988cd3c7ea2997))
* paths in no area bring the root into scope ([#44](https://github.com/modulestf/facts/issues/44)) ([bcf6051](https://github.com/modulestf/facts/commit/bcf605124e24bd199f04d986a324c7fe9890e1db))
* schema-diff follow-ups ([#40](https://github.com/modulestf/facts/issues/40)) ([d9a0ca6](https://github.com/modulestf/facts/commit/d9a0ca656c842e3c18f26ce400d730b2bdbdb977))
* scope rules are not_evaluated for a changed nested submodule directory ([#51](https://github.com/modulestf/facts/issues/51)) ([c7d8a5c](https://github.com/modulestf/facts/commit/c7d8a5c8acfc56d17572d754329888908b631bd7)), closes [#48](https://github.com/modulestf/facts/issues/48)
* scope rules are not_evaluated when a test helper changes ([#48](https://github.com/modulestf/facts/issues/48)) ([1063297](https://github.com/modulestf/facts/commit/1063297ab03da49b373ec96a1891ae62b36c7481)), closes [#44](https://github.com/modulestf/facts/issues/44)
* scope.new-submodule is not_evaluated for a new nested directory under modules/<m>/ ([#52](https://github.com/modulestf/facts/issues/52)) ([bb9a7ce](https://github.com/modulestf/facts/commit/bb9a7ceac7484253a9cd54e0db92496034ca490c)), closes [#51](https://github.com/modulestf/facts/issues/51)


### Features

* bundle builds the schema coverage section ([#45](https://github.com/modulestf/facts/issues/45)) ([7508801](https://github.com/modulestf/facts/commit/75088016975bfa9b8af2d113af070d1c93d938df))
* compat interface candidates ([#31](https://github.com/modulestf/facts/issues/31)) ([6d95ab9](https://github.com/modulestf/facts/commit/6d95ab96f8d110cc3220267debf714d912db1a8c))
* compat resource and version candidates ([#32](https://github.com/modulestf/facts/issues/32)) ([76bceba](https://github.com/modulestf/facts/commit/76bceba8d2103e9001a89dbb8a428f8c4e17d968))
* coverage reachability candidates ([#33](https://github.com/modulestf/facts/issues/33)) ([3ef36e8](https://github.com/modulestf/facts/commit/3ef36e8e6ba37fd550d6443ed9fd61c654180b2f))
* coverage rules read raw provider schema files ([#42](https://github.com/modulestf/facts/issues/42)) ([579db0d](https://github.com/modulestf/facts/commit/579db0decf08ad110de20cb469f7f56f9a3a201c))
* examples candidates ([#30](https://github.com/modulestf/facts/issues/30)) ([4abfbf9](https://github.com/modulestf/facts/commit/4abfbf9cf1147a8e5b4916715dcc7f6e1ee58125))
* mtf-facts bundle with validity predicate and injection ([#39](https://github.com/modulestf/facts/issues/39)) ([fb0c9ce](https://github.com/modulestf/facts/commit/fb0c9cee19f237cf01dff7e1fb9df42f02cb3f83))
* mtf-facts ledger ([#37](https://github.com/modulestf/facts/issues/37)) ([43e9db6](https://github.com/modulestf/facts/commit/43e9db67dc99ccbe6af39c52f8830a14e7c686ce))
* mtf-facts merge ([#38](https://github.com/modulestf/facts/issues/38)) ([2ac64f2](https://github.com/modulestf/facts/commit/2ac64f2b08f78965f2c7572e79081defb3183ab9))
* mtf-facts schema-diff ([#36](https://github.com/modulestf/facts/issues/36)) ([17ac945](https://github.com/modulestf/facts/commit/17ac9457521bdaab87615a892aa983234f83a3c0))
* profile tool rules (required file, banner, nonstandard config) ([#27](https://github.com/modulestf/facts/issues/27)) ([997e52f](https://github.com/modulestf/facts/commit/997e52f360a9e581b39f04292731072c470bdc4f))
* scope config candidates ([#34](https://github.com/modulestf/facts/issues/34)) ([d3f55fa](https://github.com/modulestf/facts/commit/d3f55fa9368632cc0a03fd04dcc41ccec8a8893e))
* structure local candidates ([#28](https://github.com/modulestf/facts/issues/28)) ([7e8e2f8](https://github.com/modulestf/facts/commit/7e8e2f88cd2e64b5ab0e7d9b6a4bf195c4a1929f))
* structure version and passthrough candidates ([#35](https://github.com/modulestf/facts/issues/35)) ([3eea9e3](https://github.com/modulestf/facts/commit/3eea9e3c7ef73823605aa265c3383e4f2843044b)), closes [structure.passthrou#default-drift](https://github.com/structure.passthrou/issues/default-drift) [structure.passthrou#field-drift](https://github.com/structure.passthrou/issues/field-drift)

# [0.2.0-rc.1](https://github.com/modulestf/facts/compare/v0.1.0-rc.1...v0.2.0-rc.1) (2026-10-22)


### Bug Fixes

* **facts:** follow-ups from the [#10](https://github.com/modulestf/facts/issues/10) review ([#12](https://github.com/modulestf/facts/issues/12)) ([7a7d871](https://github.com/modulestf/facts/commit/7a7d8715c685f880f3ff905e5936e3df07d4a43d))


### Features

* change map, touched scope and line-level cross-area scoping ([#21](https://github.com/modulestf/facts/issues/21)) ([459515e](https://github.com/modulestf/facts/commit/459515ec07006e73126c788e2ceeca35368a63c9))
* contracts for the candidate tier ([#15](https://github.com/modulestf/facts/issues/15)) ([f89679b](https://github.com/modulestf/facts/commit/f89679bfc4fce6fad4f142debfab5a2299cdc538))
* coverage candidates from schema facts ([#25](https://github.com/modulestf/facts/issues/25)) ([10b28c1](https://github.com/modulestf/facts/commit/10b28c1c0edc453a7ff46b69eaf04c42c435ee40))
* interface diff in the report ([#26](https://github.com/modulestf/facts/issues/26)) ([fd3dc61](https://github.com/modulestf/facts/commit/fd3dc618044b72c68341a1114d0d95bad727474b))
* parser model and facts index ([#17](https://github.com/modulestf/facts/issues/17)) ([d98da5e](https://github.com/modulestf/facts/commit/d98da5efc242209ade9c1b7a2720e0f22a1e5635))
* profile and scope path candidates ([#29](https://github.com/modulestf/facts/issues/29)) ([2e2e5a8](https://github.com/modulestf/facts/commit/2e2e5a8063e4ec1a9550e757ecf195b04da6fd0f))
* profile input from the profile constants blocks ([#20](https://github.com/modulestf/facts/issues/20)) ([5d8175a](https://github.com/modulestf/facts/commit/5d8175abe033d0ed6607acd58e5c78d0b449a42d))
* record raw schema file hashes without a handoff ([#24](https://github.com/modulestf/facts/issues/24)) ([e2c60f4](https://github.com/modulestf/facts/commit/e2c60f4d1d7452fb7941933ccca4dec3cb26aaae))
* registry package with every column and the dedup overlap table ([#14](https://github.com/modulestf/facts/issues/14)) ([5b45fee](https://github.com/modulestf/facts/commit/5b45feeabe9b867f24953be02956dccf12ea0140))
* schema input from the handoff and raw provider schema ([#18](https://github.com/modulestf/facts/issues/18)) ([33c9205](https://github.com/modulestf/facts/commit/33c9205f977134dbae8aab1e2356d783955fa867))

# [0.1.0-rc.1](https://github.com/modulestf/facts/compare/4ee6584d001baf9f1397fa17a225fd78aab674e9...v0.1.0-rc.1) (2026-10-21)


### Bug Fixes

* **facts:** apply owner decisions on areas, cross-area rules, links and subjects ([#8](https://github.com/modulestf/facts/issues/8)) ([d60d85d](https://github.com/modulestf/facts/commit/d60d85dcaa1b8ceb3756320df48e265852a5e247))
* **facts:** limit structure rules to root and submodule areas ([#6](https://github.com/modulestf/facts/issues/6)) ([373daf4](https://github.com/modulestf/facts/commit/373daf435564db7befb419a0e008ac34c1abf2b2))
* **facts:** read every area in a cross-area pass ([#10](https://github.com/modulestf/facts/issues/10)) ([c3726ea](https://github.com/modulestf/facts/commit/c3726ea3d793e08d78f98f9286c6e284275874a9))


### Features

* add JSON schemas for the facts contracts ([#2](https://github.com/modulestf/facts/issues/2)) ([e852046](https://github.com/modulestf/facts/commit/e8520464310ee52bc85a3d166695bf41accc8a37))
* add pinned table of Terraform core language features ([#1](https://github.com/modulestf/facts/issues/1)) ([4ee6584](https://github.com/modulestf/facts/commit/4ee6584d001baf9f1397fa17a225fd78aab674e9))
* first mtf-facts engine with lint ([#3](https://github.com/modulestf/facts/issues/3)) ([ce0c881](https://github.com/modulestf/facts/commit/ce0c881e38c85943bab6ebf8cb9d132c646038d5))
* **schema:** add merged-findings schema; host drops or caps per finding ([#7](https://github.com/modulestf/facts/issues/7)) ([394d5c0](https://github.com/modulestf/facts/commit/394d5c0ee5db2e8e0cd9b4ca05657973feb07c34))

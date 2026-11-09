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

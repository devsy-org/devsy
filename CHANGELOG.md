# Changelog

## [1.3.0-rc.10](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.9...v1.3.0-rc.10) (2026-05-18)


### Bug Fixes

* **ci:** publish stable release as latest instead of draft in promote workflow ([#344](https://github.com/devsy-org/devsy/issues/344)) ([4455eea](https://github.com/devsy-org/devsy/commit/4455eeae321a568421e26d6946260142d8504e63))
* **ci:** use GITHUB_TOKEN for release.yml dispatch in promote workflow ([#342](https://github.com/devsy-org/devsy/issues/342)) ([5b9492f](https://github.com/devsy-org/devsy/commit/5b9492f9ca888194710589b9802f8a4d6ed6e64e))

## [1.3.0-rc.9](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.8...v1.3.0-rc.9) (2026-05-18)


### Bug Fixes

* **ci:** add fetch-tags to promote-release checkout step ([#340](https://github.com/devsy-org/devsy/issues/340)) ([8d457be](https://github.com/devsy-org/devsy/commit/8d457be20695e89568c644c522cb20448f229d32))

## [1.3.0-rc.8](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.7...v1.3.0-rc.8) (2026-05-18)


### Bug Fixes

* **ci:** exclude CLI deploys from Netlify restore search ([#337](https://github.com/devsy-org/devsy/issues/337)) ([db5ecf3](https://github.com/devsy-org/devsy/commit/db5ecf392f7d91dda6d39711c1d22e47356e678c))

## [1.3.0-rc.7](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.6...v1.3.0-rc.7) (2026-05-18)


### Bug Fixes

* **ci:** deploy electron update metadata to dl.devsy.sh via Netlify ([#334](https://github.com/devsy-org/devsy/issues/334)) ([094c375](https://github.com/devsy-org/devsy/commit/094c3759fad5ad121fac2a969793aae14bb2a986))

## [1.3.0-rc.6](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.5...v1.3.0-rc.6) (2026-05-18)


### Features

* **ci:** add path-based filtering to skip e2e on non-Go changes ([#332](https://github.com/devsy-org/devsy/issues/332)) ([af92dfc](https://github.com/devsy-org/devsy/commit/af92dfc8eaf2376bf5365adf09bafafffb770f40))

## [1.3.0-rc.5](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.4...v1.3.0-rc.5) (2026-05-18)


### Bug Fixes

* **ci:** correct RC tag regex to match dot-separated format ([#328](https://github.com/devsy-org/devsy/issues/328)) ([6bdacbe](https://github.com/devsy-org/devsy/commit/6bdacbeaec13ae1c620ad3e817c38bc89f4be1c9))
* **ci:** remove Netlify prod deploy that overwrites devsy.sh ([#330](https://github.com/devsy-org/devsy/issues/330)) ([791c0e7](https://github.com/devsy-org/devsy/commit/791c0e7f55b04d835a177f3d1f7c3c9dfe9323dd))

## [1.3.0-rc.4](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.3...v1.3.0-rc.4) (2026-05-18)


### Bug Fixes

* **ci:** upload desktop artifacts via softprops/action-gh-release ([#326](https://github.com/devsy-org/devsy/issues/326)) ([6a67c26](https://github.com/devsy-org/devsy/commit/6a67c2629dca504bc2dcf9ee88b3c4b53b445aa3))

## [1.3.0-rc.3](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.2...v1.3.0-rc.3) (2026-05-18)


### Bug Fixes

* **ci:** add permissions to publish-prerelease job ([#324](https://github.com/devsy-org/devsy/issues/324)) ([2f3cdbd](https://github.com/devsy-org/devsy/commit/2f3cdbda66e68459cb26770ebcf62bff6648448c))

## [1.3.0-rc.2](https://github.com/devsy-org/devsy/compare/v1.3.0-rc.1...v1.3.0-rc.2) (2026-05-18)


### Bug Fixes

* **ci:** add --no-build flag to netlify deploy in release workflow ([#323](https://github.com/devsy-org/devsy/issues/323)) ([3c186eb](https://github.com/devsy-org/devsy/commit/3c186ebf66a693989bdef14f9ab708bb59439eda))
* **ci:** fix Netlify deploy and Flatpak upload in release workflow ([#321](https://github.com/devsy-org/devsy/issues/321)) ([642dc1c](https://github.com/devsy-org/devsy/commit/642dc1c63aba7e6ba7e61412e19cf3cc57b3a9b2))

## [1.3.0-rc.1](https://github.com/devsy-org/devsy/compare/v1.3.0-rc...v1.3.0-rc.1) (2026-05-18)


### Bug Fixes

* **ci:** pin Windows desktop builds to windows-2022 for node-gyp compat ([#319](https://github.com/devsy-org/devsy/issues/319)) ([fb38c95](https://github.com/devsy-org/devsy/commit/fb38c9505b48d9bb746340e45ae3ba22577b3b19))
* **ci:** resolve 3 desktop build failures in release workflow ([#314](https://github.com/devsy-org/devsy/issues/314)) ([4c5cea8](https://github.com/devsy-org/devsy/commit/4c5cea8fff9ebde992821710583dd54122855ce3))
* **ci:** revert CI skip condition on release-please PRs ([#320](https://github.com/devsy-org/devsy/issues/320)) ([a7110d7](https://github.com/devsy-org/devsy/commit/a7110d727f04e95e589450f432ed0a007a6cd5a2))

## [1.3.0-rc](https://github.com/devsy-org/devsy/compare/v1.2.1-rc...v1.3.0-rc) (2026-05-17)


### Features

* add workspace rename command with auto-stop and e2e tests ([#308](https://github.com/devsy-org/devsy/issues/308)) ([ff2afac](https://github.com/devsy-org/devsy/commit/ff2afac1170f863629a95850db304cb32caeb6e9))
* **desktop:** add workspace rename UI with inline editing ([#311](https://github.com/devsy-org/devsy/issues/311)) ([0038071](https://github.com/devsy-org/devsy/commit/0038071020b48fae8ba2774e1a5f6baa01db5af2))


### Bug Fixes

* **ci:** flatten downloaded CLI artifacts before desktop build ([#312](https://github.com/devsy-org/devsy/issues/312)) ([8a3b397](https://github.com/devsy-org/devsy/commit/8a3b397d6055c2f886740ffe5908a5e87b0a8a40))
* **ci:** skip network-dependent OCI tests in goreleaser pre-hook ([#309](https://github.com/devsy-org/devsy/issues/309)) ([87d893e](https://github.com/devsy-org/devsy/commit/87d893eec5765d67966a0c826a2fa8220bfb99f3))

## [1.2.1-rc](https://github.com/devsy-org/devsy/compare/v1.2.0...v1.2.1-rc) (2026-05-17)


### Bug Fixes

* **ci:** add prerelease versioning to release-please config ([#305](https://github.com/devsy-org/devsy/issues/305)) ([f65bb85](https://github.com/devsy-org/devsy/commit/f65bb8540c9a03c4bcafa95bdb247e00cdac6d81))

## [1.2.0](https://github.com/devsy-org/devsy/compare/v1.1.0...v1.2.0) (2026-05-17)


### Features

* **telemetry:** replace Loft analytics with PostHog and add Netlify update hosting ([#303](https://github.com/devsy-org/devsy/issues/303)) ([07824bd](https://github.com/devsy-org/devsy/commit/07824bdf875abefc5d51c1a1d1fe68c34bbde08f))

## [1.1.0](https://github.com/devsy-org/devsy/compare/v1.0.0...v1.1.0) (2026-05-16)


### Features

* add community provider for podman ([#453](https://github.com/devsy-org/devsy/issues/453)) ([a0efad6](https://github.com/devsy-org/devsy/commit/a0efad62c7c68b6f1385fcc803a84b9a4c6586d9))
* add support for IBM Bob IDE ([#657](https://github.com/devsy-org/devsy/issues/657)) ([5c5255d](https://github.com/devsy-org/devsy/commit/5c5255d94e9f47e2492e9dbfc12a0d48e435dda0))
* add zap-based pkg/log and CLI verbosity flags ([#13](https://github.com/devsy-org/devsy/issues/13)) ([5f05055](https://github.com/devsy-org/devsy/commit/5f05055ce3ed917c8fcc0d1cc1635c535a3e2310))
* **agent:** implement AgentDelivery interface ([#265](https://github.com/devsy-org/devsy/issues/265)) ([9d5a5b8](https://github.com/devsy-org/devsy/commit/9d5a5b8acf4969ab10a9f0b44c203b43f8ebbd15))
* **build:** add CLI --cache-from flags with priority over devcontainer.json ([#167](https://github.com/devsy-org/devsy/issues/167)) ([ecd9945](https://github.com/devsy-org/devsy/commit/ecd99451c78dc6c8e319907ef2d369ef9cab9c58))
* **cli:** add --default-user-env-probe flag to override config ([#175](https://github.com/devsy-org/devsy/issues/175)) ([77d40f5](https://github.com/devsy-org/devsy/commit/77d40f57216b030319b90fb02543aca03f5b8dc9))
* **cli:** add --docker-path flag to read-configuration command ([#245](https://github.com/devsy-org/devsy/issues/245)) ([7e53991](https://github.com/devsy-org/devsy/commit/7e53991a55dc802cb79566f79a57cc0324f7ca64))
* **cli:** add --id-label flag for custom container identification ([#183](https://github.com/devsy-org/devsy/issues/183)) ([7e61e8c](https://github.com/devsy-org/devsy/commit/7e61e8cfdebeace29b90016dcad8eb7d4d6cb6cc))
* **cli:** add --id-label flag to read-configuration command ([#244](https://github.com/devsy-org/devsy/issues/244)) ([271f84e](https://github.com/devsy-org/devsy/commit/271f84e3c860dc44a033ad31559935cea3044a7e))
* **cli:** add --result-format flag for JSON envelope control (DEVSY-022) ([#243](https://github.com/devsy-org/devsy/issues/243)) ([955dd58](https://github.com/devsy-org/devsy/commit/955dd58d6384b7d000169ac44071f9dfd4ffe220))
* **cli:** add --secrets-file flag for lifecycle command env injection ([#179](https://github.com/devsy-org/devsy/issues/179)) ([8a996c3](https://github.com/devsy-org/devsy/commit/8a996c307aacd44d7cc6ed5b5a169c9fe3904d62))
* **cli:** add exec command for container command execution ([#157](https://github.com/devsy-org/devsy/issues/157)) ([029684c](https://github.com/devsy-org/devsy/commit/029684ca8ad15c36b18cfb166171a847cc44cccb))
* **cli:** add features publish command ([#263](https://github.com/devsy-org/devsy/issues/263)) ([3bc64ed](https://github.com/devsy-org/devsy/commit/3bc64ed62cd4fedd977f7a9b01089fb5e7e1d752))
* **cli:** add features test command for isolated feature testing ([#258](https://github.com/devsy-org/devsy/issues/258)) ([a54d9ff](https://github.com/devsy-org/devsy/commit/a54d9ff1bd1faab87863072aa84def8665dcf0c9))
* **cli:** add flag aliases for devcontainer CLI compatibility ([#174](https://github.com/devsy-org/devsy/issues/174)) ([3c1fed4](https://github.com/devsy-org/devsy/commit/3c1fed43de7355eb160cdfad42592815c9f0110d))
* **cli:** add read-configuration command ([#152](https://github.com/devsy-org/devsy/issues/152)) ([bb029d0](https://github.com/devsy-org/devsy/commit/bb029d0289bdb4a0fc212051ca2021afd11a830e))
* **cli:** add repeatable --mount flag to up command ([#295](https://github.com/devsy-org/devsy/issues/295)) ([7db7af3](https://github.com/devsy-org/devsy/commit/7db7af390422693874c540ebc10ae4235aa5c300))
* **cli:** add spec-required flags to run-user-commands (DEVSY-027) ([#246](https://github.com/devsy-org/devsy/issues/246)) ([c9bca5d](https://github.com/devsy-org/devsy/commit/c9bca5d701bff20582ea5eacfe8f47df5ef5a268))
* **cli:** add upgrade command for feature versions ([#290](https://github.com/devsy-org/devsy/issues/290)) ([4e1f879](https://github.com/devsy-org/devsy/commit/4e1f879fbd537f0b516166a7eada4d3e230231b2))
* **cli:** implement features package command ([#260](https://github.com/devsy-org/devsy/issues/260)) ([d074633](https://github.com/devsy-org/devsy/commit/d074633ef4ccfff1a2cca8ee49b7dc671f551bc3))
* **cli:** rewrite set-up command to full devcontainer spec compliance ([#251](https://github.com/devsy-org/devsy/issues/251)) ([9563e32](https://github.com/devsy-org/devsy/commit/9563e3256aaa51b5f47dce1cef84cdc66918553b))
* **cmd:** add --container-id flag to read-configuration ([#205](https://github.com/devsy-org/devsy/issues/205)) ([198dee1](https://github.com/devsy-org/devsy/commit/198dee1f6b07892dd3f9225ab6a1d459e5d69477))
* **cmd:** add --remove-volumes flag to down command ([#207](https://github.com/devsy-org/devsy/issues/207)) ([ba9fbb9](https://github.com/devsy-org/devsy/commit/ba9fbb92534b8f4e56bf54d297bbbfa48d035288))
* **cmd:** add --update-remote-user-uid-default flag to up command ([#208](https://github.com/devsy-org/devsy/issues/208)) ([14e6944](https://github.com/devsy-org/devsy/commit/14e6944dc1215d83a641f17887bd971e53ccb3b9))
* **cmd:** add --workspace-mount-consistency flag to up command ([#198](https://github.com/devsy-org/devsy/issues/198)) ([e2992f8](https://github.com/devsy-org/devsy/commit/e2992f8a2b7a031ea4156acbd3cc2a65ec1998de))
* **cmd:** add `--additional-features` flag for feature injection ([#626](https://github.com/devsy-org/devsy/issues/626)) ([265b290](https://github.com/devsy-org/devsy/commit/265b290b3653b7021cfd8649ab2d61952d4c376d))
* **cmd:** add `outdated` command to check for newer feature versions ([#191](https://github.com/devsy-org/devsy/issues/191)) ([43fba26](https://github.com/devsy-org/devsy/commit/43fba2676a3fc071edc3d417e4b16e58775bc637))
* **cmd:** add `set-up` command for BYOC container configuration ([#196](https://github.com/devsy-org/devsy/issues/196)) ([3eb488e](https://github.com/devsy-org/devsy/commit/3eb488e36b736b66e306a166e646815567ac913b))
* **cmd:** add features info/resolve-deps/generate-docs commands ([#219](https://github.com/devsy-org/devsy/issues/219)) ([be8aeac](https://github.com/devsy-org/devsy/commit/be8aeac00425cf3a260785084bb8f0403e8f3836))
* **cmd:** add generic TERM compatibility modes ([#490](https://github.com/devsy-org/devsy/issues/490)) ([42e54c2](https://github.com/devsy-org/devsy/commit/42e54c234a8b88ed0b0b5095ab5f0efbef1fda5e))
* **cmd:** add hidden flag aliases for devcontainer CLI compat ([#200](https://github.com/devsy-org/devsy/issues/200)) ([7af1be2](https://github.com/devsy-org/devsy/commit/7af1be222ccd5aad462ce4064abfc98463275509))
* **cmd:** add JSON result envelope on stdout for up/build/exec ([#199](https://github.com/devsy-org/devsy/issues/199)) ([75cec78](https://github.com/devsy-org/devsy/commit/75cec78cc88962fa8db193c25261ff42aaffa0c1))
* **cmd:** add minor CLI flags ([#217](https://github.com/devsy-org/devsy/issues/217)) ([7d056fb](https://github.com/devsy-org/devsy/commit/7d056fbb7cf986d62029b7fc91027c4d96d4d115))
* **cmd:** add newline separator to version output ([#425](https://github.com/devsy-org/devsy/issues/425)) ([d24f93c](https://github.com/devsy-org/devsy/commit/d24f93cfbdef1298071521d9ba40ef2cba3b1548))
* **cmd:** add remaining minor flags ([#215](https://github.com/devsy-org/devsy/issues/215)) ([ec85a11](https://github.com/devsy-org/devsy/commit/ec85a119fd35f5e5f273157ecadf043be937cc20))
* **cmd:** add run-user-commands lifecycle command ([#203](https://github.com/devsy-org/devsy/issues/203)) ([d2ca833](https://github.com/devsy-org/devsy/commit/d2ca8330b622b4f9c501027d86e7c2fdeefb639b))
* **cmd:** add templates apply/publish/metadata/generate-docs commands ([#220](https://github.com/devsy-org/devsy/issues/220)) ([84d21c6](https://github.com/devsy-org/devsy/commit/84d21c6993ce6748024d13669456d246549c3439))
* **cmd:** rename `upgrade` command to `self-update` ([#192](https://github.com/devsy-org/devsy/issues/192)) ([bb0c72f](https://github.com/devsy-org/devsy/commit/bb0c72f1689942f457582429a4ae48f2e74dedd7))
* **cmd:** retrieve virtual machine instance description ([#602](https://github.com/devsy-org/devsy/issues/602)) ([1451fa1](https://github.com/devsy-org/devsy/commit/1451fa12b1b350bc1c3d1e19355d5b21ddfad9ba))
* **compose:** add podman compose detection path ([#274](https://github.com/devsy-org/devsy/issues/274)) ([774ab5d](https://github.com/devsy-org/devsy/commit/774ab5d5941805fee431bb651d3cd46f75cdc48d))
* **compose:** validate hostRequirements in Docker Compose path ([#247](https://github.com/devsy-org/devsy/issues/247)) ([7cd689f](https://github.com/devsy-org/devsy/commit/7cd689f30fc08f350b3a2d3284c08913912822a8))
* **compose:** validate runServices against compose file services ([#211](https://github.com/devsy-org/devsy/issues/211)) ([db7f18a](https://github.com/devsy-org/devsy/commit/db7f18a0795ced9f631ade8e6e5714b673bde365))
* **config:** add hostRequirements pre-flight validation ([#173](https://github.com/devsy-org/devsy/issues/173)) ([82cf6b0](https://github.com/devsy-org/devsy/commit/82cf6b0ba88d3849379f54e39c77b9da4ed3dc36))
* **config:** add local-path extends support for devcontainer.json ([#184](https://github.com/devsy-org/devsy/issues/184)) ([b51dbf6](https://github.com/devsy-org/devsy/commit/b51dbf60143ad3b11a704fac332fbc8295cc69ec))
* **config:** add oci:// prefix, multi-cloud auth, and digest caching to OCI extends ([#253](https://github.com/devsy-org/devsy/issues/253)) ([80f6170](https://github.com/devsy-org/devsy/commit/80f61702d9aea4375a98e46ce961223a3cd2545c))
* **config:** add PathManager for XDG-compliant path computation ([#74](https://github.com/devsy-org/devsy/issues/74)) ([e477cf8](https://github.com/devsy-org/devsy/commit/e477cf83291451cc815ae0b9106158927f4976af))
* **config:** add securityOpt passthrough to container create ([#169](https://github.com/devsy-org/devsy/issues/169)) ([3ef60f9](https://github.com/devsy-org/devsy/commit/3ef60f92456505e834fbfc6e7a7d5d5418fa4eb9))
* **config:** derive devcontainerId from workspace folder per spec ([#195](https://github.com/devsy-org/devsy/issues/195)) ([d1895c5](https://github.com/devsy-org/devsy/commit/d1895c5368ae09ca9dabd093d04f2acbcedc2f84))
* **config:** enforce phase-aware variable substitution scoping ([#249](https://github.com/devsy-org/devsy/issues/249)) ([8f74302](https://github.com/devsy-org/devsy/commit/8f743022838e20555a263710505aec2ecb668249))
* **config:** implement spec-compliant devcontainerId derivation ([#252](https://github.com/devsy-org/devsy/issues/252)) ([d2ee4ab](https://github.com/devsy-org/devsy/commit/d2ee4ab6ca2b9b7bd668434f29b2ccbf8f4282a4))
* **config:** parse and resolve devcontainer secrets property ([#83](https://github.com/devsy-org/devsy/issues/83)) ([64a6d4f](https://github.com/devsy-org/devsy/commit/64a6d4f52775a76ba480efb95e401fd2814ece23))
* **config:** resolve variable substitution in extends paths ([#256](https://github.com/devsy-org/devsy/issues/256)) ([e73498e](https://github.com/devsy-org/devsy/commit/e73498e77a2d4c638e042b7a12d94c7abcdc3895))
* **config:** support array form for extends property ([#188](https://github.com/devsy-org/devsy/issues/188)) ([da42f37](https://github.com/devsy-org/devsy/commit/da42f37fedad1ddf9ede0f20835290480c869272))
* **config:** support custom workspaceMount from devcontainer.json ([#170](https://github.com/devsy-org/devsy/issues/170)) ([a7794f0](https://github.com/devsy-org/devsy/commit/a7794f01eff445528eddfc9f5695521f7b8d0ccf))
* **config:** support forwardPorts range syntax expansion ([#168](https://github.com/devsy-org/devsy/issues/168)) ([1c618f1](https://github.com/devsy-org/devsy/commit/1c618f1b9b40a985783723b9b3659e05f5d471d6))
* **config:** support OCI remote extends for devcontainer.json ([#190](https://github.com/devsy-org/devsy/issues/190)) ([8e1e7dd](https://github.com/devsy-org/devsy/commit/8e1e7ddc0d396d6d1f70578ff5ec20d5e4e581e3))
* **delivery:** add KubernetesDelivery strategy for K8s agent binary injection ([#267](https://github.com/devsy-org/devsy/issues/267)) ([918ed99](https://github.com/devsy-org/devsy/commit/918ed99ea1331bfa1f0c9a8886d2ea91ead931a6))
* **desktop:** add Electron desktop app ([#298](https://github.com/devsy-org/devsy/issues/298)) ([b01eae3](https://github.com/devsy-org/devsy/commit/b01eae3a2cd79a00e3aab21b19b5892a79caf303))
* **devcontainer/setup:** open IDE before postAttachCommand runs ([#728](https://github.com/devsy-org/devsy/issues/728)) ([0ba9c1a](https://github.com/devsy-org/devsy/commit/0ba9c1aa1c47cc1fd80edaa75b2b410648ce76cd))
* **devcontainer:** add stopCompose shutdownAction for compose containers ([#186](https://github.com/devsy-org/devsy/issues/186)) ([9ab288f](https://github.com/devsy-org/devsy/commit/9ab288f9ebf0d57241e0a5717e73a2f8374dd367))
* **dockercredentials:** add Docker credential helper for agent ([#428](https://github.com/devsy-org/devsy/issues/428)) ([90dbaad](https://github.com/devsy-org/devsy/commit/90dbaad38d717f493c9fff82287f1e4089e5f423))
* **driver/docker:** execute docker run in the workspace directory ([#498](https://github.com/devsy-org/devsy/issues/498)) ([19dbf79](https://github.com/devsy-org/devsy/commit/19dbf79a71ec2229e1bdaf1c6ac29c7d374ca7b7))
* **driver:** guard BuildKit strategy against Podman runtime ([#270](https://github.com/devsy-org/devsy/issues/270)) ([01ef390](https://github.com/devsy-org/devsy/commit/01ef3909f1bf13a6a1496da63a5fe4bf6ab0da7c))
* enable renaming providers ([#358](https://github.com/devsy-org/devsy/issues/358)) ([1c2b543](https://github.com/devsy-org/devsy/commit/1c2b54389f55bc429a94b8397cfc637a797fe738))
* enforce hostRequirements validation with GPU support ([#292](https://github.com/devsy-org/devsy/issues/292)) ([9f49d77](https://github.com/devsy-org/devsy/commit/9f49d77515c99d3b29a022aca5bd43e749146151))
* **envelope:** surface hostRequirements warnings in CLI result JSON ([#204](https://github.com/devsy-org/devsy/issues/204)) ([c62b068](https://github.com/devsy-org/devsy/commit/c62b068c30147784793194538f1eef4dde52af3d))
* **exec:** add devcontainer spec compliance (workdir, user, remoteEnv, userEnvProbe) ([#162](https://github.com/devsy-org/devsy/issues/162)) ([94b3b9e](https://github.com/devsy-org/devsy/commit/94b3b9e0681c36ede40d568906e05e3c19114b68))
* expand user home directory references in workspace source ([#499](https://github.com/devsy-org/devsy/issues/499)) ([15c75cc](https://github.com/devsy-org/devsy/commit/15c75cc93adfa030d64304e19dbc5c4488083c86))
* expose compose project name as env var in containers ([#711](https://github.com/devsy-org/devsy/issues/711)) ([36c10c9](https://github.com/devsy-org/devsy/commit/36c10c9b8438d052339c24734f48554bd1c975ab)), closes [#456](https://github.com/devsy-org/devsy/issues/456)
* **feature:** add interactive prompting for secret options ([#291](https://github.com/devsy-org/devsy/issues/291)) ([6035a26](https://github.com/devsy-org/devsy/commit/6035a26517d3346ea11d466ba892e102c9f4654c))
* **feature:** add OCI pull retry with URL sanitization ([#176](https://github.com/devsy-org/devsy/issues/176)) ([bce287e](https://github.com/devsy-org/devsy/commit/bce287ea912b5da71b0113fb2607ca8b820c9e6e))
* **feature:** consume collection.json from OCI feature registries ([#214](https://github.com/devsy-org/devsy/issues/214)) ([80e7b08](https://github.com/devsy-org/devsy/commit/80e7b08331020bbf3e15a394df9d53d39f230e25))
* **feature:** implement secret option handling ([#213](https://github.com/devsy-org/devsy/issues/213)) ([77431d7](https://github.com/devsy-org/devsy/commit/77431d7d7be644dd42ca5e48235c745ea5729cd1))
* **feature:** parse OCI annotations from feature manifests ([#218](https://github.com/devsy-org/devsy/issues/218)) ([4208400](https://github.com/devsy-org/devsy/commit/42084009b4ff6ac825a215020378c735493afcfe))
* **feature:** resolve legacy IDs during dependency resolution ([#206](https://github.com/devsy-org/devsy/issues/206)) ([f9dc6e3](https://github.com/devsy-org/devsy/commit/f9dc6e3d9899bc71ca247db575a75a93fbebb403))
* **features:** add info manifest and info tags subcommands ([#289](https://github.com/devsy-org/devsy/issues/289)) ([2d4b112](https://github.com/devsy-org/devsy/commit/2d4b112b5fd90f84e9e5ad43e31494ad90352a0c))
* **feature:** validate option type and enum constraints at install time ([#187](https://github.com/devsy-org/devsy/issues/187)) ([836062d](https://github.com/devsy-org/devsy/commit/836062dc024000acb15fe9ff98a145631c424664))
* **feature:** version-aware feature equality ([#210](https://github.com/devsy-org/devsy/issues/210)) ([9f01c25](https://github.com/devsy-org/devsy/commit/9f01c25a0b89423cdd1e92a7c52eea02920d9797))
* **git:** support includeIf conditional config via workspace directory context ([#296](https://github.com/devsy-org/devsy/issues/296)) ([6ef0418](https://github.com/devsy-org/devsy/commit/6ef0418fe100087a38d04ccf8cf9d7c948dfd6f1))
* **graph:** implement round-based topological sort ([#201](https://github.com/devsy-org/devsy/issues/201)) ([7ddcfca](https://github.com/devsy-org/devsy/commit/7ddcfcac2a1e486292d2dc35ed95aebe749e93eb))
* **ide/vscode:** improve VS Code server discovery ([#673](https://github.com/devsy-org/devsy/issues/673)) ([9f49d94](https://github.com/devsy-org/devsy/commit/9f49d9417f7af4d0d5f7aed0da03528ba56ca071)), closes [#639](https://github.com/devsy-org/devsy/issues/639)
* **metadata:** include containerEnv in label and warn on size limit ([#178](https://github.com/devsy-org/devsy/issues/178)) ([a148fb3](https://github.com/devsy-org/devsy/commit/a148fb3a1a90e46c85afee92f0dc9590da08de68))
* **netstat:** wire portsAttributes into port forwarding watcher ([#202](https://github.com/devsy-org/devsy/issues/202)) ([2e93da4](https://github.com/devsy-org/devsy/commit/2e93da4487d472661d645906de23e3686888d8fd))
* **obs:** add injection pipeline timing logs ([#147](https://github.com/devsy-org/devsy/issues/147)) ([6773838](https://github.com/devsy-org/devsy/commit/677383868ecc474d7cc0e6809a4cd70980ed8a22))
* **port:** accept hostnames in SSH port forwarding ([#294](https://github.com/devsy-org/devsy/issues/294)) ([6832043](https://github.com/devsy-org/devsy/commit/6832043454f23ca70c0b9c05e1d22cf5fa2e0144))
* **ports:** wire portsAttributes into forwarding decisions ([#248](https://github.com/devsy-org/devsy/issues/248)) ([90dee39](https://github.com/devsy-org/devsy/commit/90dee393b062cbe4e1e70592a0b6a6cd1f586f33))
* **provider:** add built-in Podman provider ([#277](https://github.com/devsy-org/devsy/issues/277)) ([afbfd81](https://github.com/devsy-org/devsy/commit/afbfd81e13e1612fc647ea681cce02f834958aeb))
* rebrand devpod/loft to devsy ([#1](https://github.com/devsy-org/devsy/issues/1)) ([f809604](https://github.com/devsy-org/devsy/commit/f8096040b681b00f2c6f2d42f76cec7eb970fc95))
* replace PrintTable with lipgloss table renderer ([#716](https://github.com/devsy-org/devsy/issues/716)) ([6d6af55](https://github.com/devsy-org/devsy/commit/6d6af5546264b17a65d73e0ce6998c6a9eaaf2e6))
* rewrite module deps from loft-sh to skevetter forks ([#726](https://github.com/devsy-org/devsy/issues/726)) ([28b39c2](https://github.com/devsy-org/devsy/commit/28b39c2df6318c51a5bf77131e94a46d0defdfee))
* **runtime:** add ContainerRuntime abstraction interface ([#281](https://github.com/devsy-org/devsy/issues/281)) ([d74b492](https://github.com/devsy-org/devsy/commit/d74b492d20e72d23cd5bead58c042a55be9e15b5))
* **tunnel:** add PipeBridge and ConnError primitives ([#137](https://github.com/devsy-org/devsy/issues/137)) ([395b50c](https://github.com/devsy-org/devsy/commit/395b50c77fb18aeaa58fa7d95234844874df600d))
* **tunnel:** apply otherPortsAttributes defaults to unlisted ports ([#171](https://github.com/devsy-org/devsy/issues/171)) ([7828ec3](https://github.com/devsy-org/devsy/commit/7828ec3f263262c0ea2d2b2ffb3a69e735b9299e))
* **tunnel:** extract PipeBridge and ConnError primitives, fix direct.go race ([#91](https://github.com/devsy-org/devsy/issues/91)) ([d639e53](https://github.com/devsy-org/devsy/commit/d639e537743f03a38fdf63f3f5849fd5bede1cfb))
* **tunnel:** wire portsAttributes per-port settings into forwarding ([#172](https://github.com/devsy-org/devsy/issues/172)) ([11158bb](https://github.com/devsy-org/devsy/commit/11158bb67fb47232100f688af4386e4aa6c40afd))
* **ui:** remove try devpod pro button and setting ([#463](https://github.com/devsy-org/devsy/issues/463)) ([62895c9](https://github.com/devsy-org/devsy/commit/62895c927f1609963ab327390bb6d27bdcd1ea92))
* **up:** add --gpu-availability flag to override GPU detection ([#185](https://github.com/devsy-org/devsy/issues/185)) ([92b3cd7](https://github.com/devsy-org/devsy/commit/92b3cd7b5dec9da0a46a00cb067c4bc8ce1b7d27))
* **up:** add --prebuild flag for devcontainer prebuild lifecycle ([#80](https://github.com/devsy-org/devsy/issues/80)) ([e6974ff](https://github.com/devsy-org/devsy/commit/e6974ff773f7ea5d78977ba8314c5cbb4814086e))


### Bug Fixes

* **agent/git_ssh_signature:** remove agent-forwarding and start-services flags ([#662](https://github.com/devsy-org/devsy/issues/662)) ([2738ef0](https://github.com/devsy-org/devsy/commit/2738ef005e5eb802fc2fdaf21456a02fa659e0d7))
* **agent/git_ssh_signature:** ssh signature forwarding fails when signing ([#648](https://github.com/devsy-org/devsy/issues/648)) ([c52702b](https://github.com/devsy-org/devsy/commit/c52702b53d8f9de5caa874dab0ea2b8124c1fea5))
* **build:** pass environment variables to flatpak-spawn wrapper ([#640](https://github.com/devsy-org/devsy/issues/640)) ([f370b5d](https://github.com/devsy-org/devsy/commit/f370b5d926a071735f78696b8c891eee2e5e25ce))
* **build:** pass metadata labels to docker buildx build command ([#82](https://github.com/devsy-org/devsy/issues/82)) ([8d6d7c8](https://github.com/devsy-org/devsy/commit/8d6d7c8e5f140e0ac533e89d536c9fef1f493c8c))
* **build:** wire devcontainer.json cacheFrom into docker build args ([#163](https://github.com/devsy-org/devsy/issues/163)) ([61b144a](https://github.com/devsy-org/devsy/commit/61b144a729bc7dcbcc311592cc50d7e00f449750))
* capitalization of type bug in report ([#667](https://github.com/devsy-org/devsy/issues/667)) ([cfdbee4](https://github.com/devsy-org/devsy/commit/cfdbee411e05de8802bd5217b00712b6ed230c9b))
* **ci:** disable rust-cache bin caching to fix macOS cargo resolution ([#288](https://github.com/devsy-org/devsy/issues/288)) ([bf7762d](https://github.com/devsy-org/devsy/commit/bf7762de0c3655f7bb084b440dffcb424d6cacae))
* **ci:** install kind directly on Windows to avoid docker-desktop dep ([#261](https://github.com/devsy-org/devsy/issues/261)) ([e06bcf5](https://github.com/devsy-org/devsy/commit/e06bcf56e861d8853498afd0836a0fb0c7be0f3b))
* **ci:** use goreleaser-action in act workflow to avoid Go version mismatch ([#122](https://github.com/devsy-org/devsy/issues/122)) ([c1ffa70](https://github.com/devsy-org/devsy/commit/c1ffa70b4b9ce851f9e0ca76915c8b8447e6ad79))
* **cli:** make down command stop and delete containers per spec ([#153](https://github.com/devsy-org/devsy/issues/153)) ([1b24641](https://github.com/devsy-org/devsy/commit/1b24641886d2b91679b1aae358eb10323c7cc30d))
* **cmd/machine:** linting errors ([#505](https://github.com/devsy-org/devsy/issues/505)) ([33e565f](https://github.com/devsy-org/devsy/commit/33e565f7701080c55d0d6ccf83934777dad1cfdc))
* **cmd/ssh:** default ssh workdir to merged workspaceFolder ([#517](https://github.com/devsy-org/devsy/issues/517)) ([288c56d](https://github.com/devsy-org/devsy/commit/288c56d879dbf6fb0fc0fbd6d0cd79e503c3cffd))
* **cmd:** GPG agent forwarding fails with SSH signing keys ([#732](https://github.com/devsy-org/devsy/issues/732)) ([fdd4906](https://github.com/devsy-org/devsy/commit/fdd4906d53191efbbbe226b853efea7c8d3abc81))
* **compose:** podman with docker-compose stderr handling ([#618](https://github.com/devsy-org/devsy/issues/618)) ([aa90f6d](https://github.com/devsy-org/devsy/commit/aa90f6d82bd62c722e3e62df2c6d667ef7fccd26))
* **compose:** surface helper error messages ([#462](https://github.com/devsy-org/devsy/issues/462)) ([b12f407](https://github.com/devsy-org/devsy/commit/b12f407aafa41e13f8a13b0b204a568612d71f42))
* **config:** correct lifecycle hook merge ordering ([#236](https://github.com/devsy-org/devsy/issues/236)) ([a640dba](https://github.com/devsy-org/devsy/commit/a640dba777ec2098b0803f3f6166d6235be7c8b0))
* **config:** correct portsAttributes JSON struct tag typo ([#81](https://github.com/devsy-org/devsy/issues/81)) ([93e0da3](https://github.com/devsy-org/devsy/commit/93e0da3f53ed403694c188479eda518f46c81ba9))
* **config:** handle remoteEnv null values to unset variables ([#93](https://github.com/devsy-org/devsy/issues/93)) ([893db05](https://github.com/devsy-org/devsy/commit/893db0581dc9e36bbc91bf3bcb6412b092aa9811))
* **config:** make shutdownAction defaults explicit in config resolution ([#259](https://github.com/devsy-org/devsy/issues/259)) ([01b5b82](https://github.com/devsy-org/devsy/commit/01b5b824969a6f37257a008323fe4114f017be85))
* **config:** order feature hooks before image hooks per spec ([#197](https://github.com/devsy-org/devsy/issues/197)) ([d396b6b](https://github.com/devsy-org/devsy/commit/d396b6b30abfc7c6a7cc4cbbacc99ed4ec6ff7c4))
* **config:** preserve colons in variable substitution default values ([#9](https://github.com/devsy-org/devsy/issues/9)) ([#94](https://github.com/devsy-org/devsy/issues/94)) ([97f2d96](https://github.com/devsy-org/devsy/commit/97f2d96aa4ac606a5c61029b822e0f717368a370))
* **config:** remove unsupported ${env:VAR} variable substitution ([#194](https://github.com/devsy-org/devsy/issues/194)) ([b79d806](https://github.com/devsy-org/devsy/commit/b79d806f6ebdb469ef0971e908e0e2b30d0e7ab6))
* **config:** resolve undefined variables to empty string ([#96](https://github.com/devsy-org/devsy/issues/96)) ([3701855](https://github.com/devsy-org/devsy/commit/37018553b144fd706ae4a943de793769f2b78d14))
* **config:** support hostRequirements.gpu object format ([#117](https://github.com/devsy-org/devsy/issues/117)) ([377bac9](https://github.com/devsy-org/devsy/commit/377bac981152f26b3d441df44fe55d2f55c097da))
* **config:** suppress workspace mount when workspaceMount is empty string ([#283](https://github.com/devsy-org/devsy/issues/283)) ([#287](https://github.com/devsy-org/devsy/issues/287)) ([2992939](https://github.com/devsy-org/devsy/commit/2992939fbfe4b75545cf84cb458ce2386a770024))
* **config:** union hostRequirements across merge sources ([#121](https://github.com/devsy-org/devsy/issues/121)) ([b66c2a1](https://github.com/devsy-org/devsy/commit/b66c2a1029816bf748ac71fe26b19fea8baa75bd))
* **conn:** plug goroutine leaks and WaitGroup race in ssh/agent packages ([#126](https://github.com/devsy-org/devsy/issues/126)) ([93c54e3](https://github.com/devsy-org/devsy/commit/93c54e31f72da1939f9797c7c26dc64302e28708))
* **conn:** plug goroutine leaks and WaitGroup race in ssh/agent packages ([#90](https://github.com/devsy-org/devsy/issues/90)) ([b05020d](https://github.com/devsy-org/devsy/commit/b05020d6592e70d1d0223019fab027422e877876))
* **credentials:** surface error responses from credential server endpoint ([#694](https://github.com/devsy-org/devsy/issues/694)) ([f2b481f](https://github.com/devsy-org/devsy/commit/f2b481fa73c1562aa58677e031b7197b32c8b23e)), closes [#645](https://github.com/devsy-org/devsy/issues/645)
* **daemon:** enforce shutdownAction property at runtime ([#88](https://github.com/devsy-org/devsy/issues/88)) ([ea9f774](https://github.com/devsy-org/devsy/commit/ea9f774c66d6f46d1335abe345e26772fd48fdac))
* **daemon:** trust merge layer for ShutdownAction instead of re-defaulting ([#286](https://github.com/devsy-org/devsy/issues/286)) ([32e062a](https://github.com/devsy-org/devsy/commit/32e062a778aa4dbed5072566d3d7a1fb018ed82d))
* **delivery:** clean up agent delivery volumes on workspace deletion ([#266](https://github.com/devsy-org/devsy/issues/266)) ([c75008c](https://github.com/devsy-org/devsy/commit/c75008cce548e32c4667504e3a4f7a70925ca6c9))
* **delivery:** ensure target dir exists before docker cp ([#269](https://github.com/devsy-org/devsy/issues/269)) ([b39c2b4](https://github.com/devsy-org/devsy/commit/b39c2b4800cf81ca1d251e4a5110f37fb7487325))
* **delivery:** support rootless Podman in volume direct copy ([#275](https://github.com/devsy-org/devsy/issues/275)) ([f489ccb](https://github.com/devsy-org/devsy/commit/f489ccb469174421a7d90ceebfda28822a600919))
* **deps:** remove programming-language-detection dependency ([#695](https://github.com/devsy-org/devsy/issues/695)) ([bdc7124](https://github.com/devsy-org/devsy/commit/bdc7124b1d61de445581adc06d00d75433185a7e))
* **deps:** update dependency @loft-enterprise/client to v4 ([#506](https://github.com/devsy-org/devsy/issues/506)) ([987ffad](https://github.com/devsy-org/devsy/commit/987ffad621734b3f6c4208d659241eff461c017e))
* **deps:** update dependency @loft-enterprise/client to v4 ([#600](https://github.com/devsy-org/devsy/issues/600)) ([475efca](https://github.com/devsy-org/devsy/commit/475efca753945e6021e597147c20156ce44cee4c))
* **deps:** update dependency @tauri-apps/plugin-shell to v2.3.5 ([#438](https://github.com/devsy-org/devsy/issues/438)) ([d7226e6](https://github.com/devsy-org/devsy/commit/d7226e680c47a99350b1f9e644514c8cdb734aa9))
* **deps:** update dependency @tauri-apps/plugin-updater to v2.10.0 ([#441](https://github.com/devsy-org/devsy/issues/441)) ([19087c1](https://github.com/devsy-org/devsy/commit/19087c187b559c2ca7705573dc664c30c52a24fa))
* **deps:** update dependency react-hook-form to v7.71.2 ([#562](https://github.com/devsy-org/devsy/issues/562)) ([6a6158a](https://github.com/devsy-org/devsy/commit/6a6158a375877c3f3652cbe9a5fed085a2b15a64))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 0794660 ([#651](https://github.com/devsy-org/devsy/issues/651)) ([bcb9091](https://github.com/devsy-org/devsy/commit/bcb9091790e78c09ae2c8d2667a54d7f4b966481))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 3888fb8 ([#621](https://github.com/devsy-org/devsy/issues/621)) ([870e6e3](https://github.com/devsy-org/devsy/commit/870e6e31e338fcff01a35d7663de78a11cfc8cd5))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 400c263 ([#620](https://github.com/devsy-org/devsy/issues/620)) ([ebbc373](https://github.com/devsy-org/devsy/commit/ebbc373ca484810a884809b32f35979cff68fb1e))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 47eedc9 ([#619](https://github.com/devsy-org/devsy/issues/619)) ([a27dae5](https://github.com/devsy-org/devsy/commit/a27dae5cc55599c919b4e91911515891055de2a3))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 5b80281 ([#715](https://github.com/devsy-org/devsy/issues/715)) ([7873b67](https://github.com/devsy-org/devsy/commit/7873b67e7f5448fc0d72ca9dd51e224f4cd29310))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 7a66278 ([#737](https://github.com/devsy-org/devsy/issues/737)) ([e78e2b7](https://github.com/devsy-org/devsy/commit/e78e2b740820aea175c11815ea0385dc4b0464b7))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 85f2bf5 ([#538](https://github.com/devsy-org/devsy/issues/538)) ([91a3cf5](https://github.com/devsy-org/devsy/commit/91a3cf57784de0e9e45a38ae0e4bfa609b9755d3))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 93aa273 ([#439](https://github.com/devsy-org/devsy/issues/439)) ([2866ef7](https://github.com/devsy-org/devsy/commit/2866ef7d75acf53bb508e093cc076c36227644c7))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to 9e0ccb0 ([#541](https://github.com/devsy-org/devsy/issues/541)) ([b685929](https://github.com/devsy-org/devsy/commit/b685929d1a63f6de1243af1dcda6389a7a494188))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to b6eadd8 ([#474](https://github.com/devsy-org/devsy/issues/474)) ([4745c03](https://github.com/devsy-org/devsy/commit/4745c03610eca98c4551f604185f400633145afc))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to bf0f710 ([#641](https://github.com/devsy-org/devsy/issues/641)) ([219fe9d](https://github.com/devsy-org/devsy/commit/219fe9d9f0ea724d2e47dd219455f76400d18395))
* **deps:** update github.com/google/go-containerregistry/pkg/authn/kubernetes digest to e90447d ([#668](https://github.com/devsy-org/devsy/issues/668)) ([0d60f4b](https://github.com/devsy-org/devsy/commit/0d60f4b1384999d1d73a25fb73390d128a79d4ff))
* **deps:** update k8s.io/utils digest to 28399d8 ([#631](https://github.com/devsy-org/devsy/issues/631)) ([f4fd4e9](https://github.com/devsy-org/devsy/commit/f4fd4e91a07bea418a25fb2b3e080dca35420700))
* **deps:** update k8s.io/utils digest to b8788ab ([#467](https://github.com/devsy-org/devsy/issues/467)) ([eac3511](https://github.com/devsy-org/devsy/commit/eac351116e1827a3e1c391ded32e988c12dcb595))
* **deps:** update kubernetes monorepo to v0.35.3 ([#632](https://github.com/devsy-org/devsy/issues/632)) ([d40d249](https://github.com/devsy-org/devsy/commit/d40d249587c4ba4469422a64011b9dcf1409b58d))
* **deps:** update kubernetes packages to v0.35.1 ([#468](https://github.com/devsy-org/devsy/issues/468)) ([38b3cfd](https://github.com/devsy-org/devsy/commit/38b3cfd01a4ea2d6b5c77fe39b4eb9707564d1a5))
* **deps:** update kubernetes packages to v0.35.2 ([#539](https://github.com/devsy-org/devsy/issues/539)) ([212b625](https://github.com/devsy-org/devsy/commit/212b6254140b4b3cd20195d49d657ab285cc83b1))
* **deps:** update module charm.land/lipgloss/v2 to v2.0.2 ([#727](https://github.com/devsy-org/devsy/issues/727)) ([2613a0f](https://github.com/devsy-org/devsy/commit/2613a0f3220e0895ab22238edb71b49e9859b5b0))
* **deps:** update module charm.land/lipgloss/v2 to v2.0.3 ([#735](https://github.com/devsy-org/devsy/issues/735)) ([f28dcb1](https://github.com/devsy-org/devsy/commit/f28dcb1b33701e02e45f403de11f32abedd8f0db))
* **deps:** update module github.com/awslabs/amazon-ecr-credential-helper/ecr-login to v0.12.0 ([#540](https://github.com/devsy-org/devsy/issues/540)) ([5f473e2](https://github.com/devsy-org/devsy/commit/5f473e2b7aa4f63cd0c07a735069d5113876c84f))
* **deps:** update module github.com/azure/azure-sdk-for-go/sdk/azcore to v1.21.0 ([#718](https://github.com/devsy-org/devsy/issues/718)) ([7e38d0a](https://github.com/devsy-org/devsy/commit/7e38d0aaf68949525c8317cf14504bc47d0d1087))
* **deps:** update module github.com/charmbracelet/huh to v2 ([#723](https://github.com/devsy-org/devsy/issues/723)) ([3b87242](https://github.com/devsy-org/devsy/commit/3b872424436d2c628663973eb2e1c251a292fc0d))
* **deps:** update module github.com/compose-spec/compose-go/v2 to v2.10.2 ([#672](https://github.com/devsy-org/devsy/issues/672)) ([9c397fe](https://github.com/devsy-org/devsy/commit/9c397fe38b69683a547f4000c4487a922d6d7d7b))
* **deps:** update module github.com/docker/cli to v29.3.0+incompatible ([#580](https://github.com/devsy-org/devsy/issues/580)) ([991462d](https://github.com/devsy-org/devsy/commit/991462d7a7bfcb0a2d4b7158c77a4a9698e35d0d))
* **deps:** update module github.com/google/go-containerregistry to v0.21.2 ([#543](https://github.com/devsy-org/devsy/issues/543)) ([12c6b63](https://github.com/devsy-org/devsy/commit/12c6b63f7c886e3608e06ed74f2a0ad56a6f8a9a))
* **deps:** update module github.com/google/go-containerregistry to v0.21.3 ([#622](https://github.com/devsy-org/devsy/issues/622)) ([fc4b602](https://github.com/devsy-org/devsy/commit/fc4b602287e578ee467c2c4bc89881c020ec69e3))
* **deps:** update module github.com/google/go-containerregistry to v0.21.5 ([#717](https://github.com/devsy-org/devsy/issues/717)) ([ed04623](https://github.com/devsy-org/devsy/commit/ed046239f2b12a2cc7c7409c7c933e93c7225bdb))
* **deps:** update module github.com/loft-sh/agentapi/v4 to v4.7.0 ([#547](https://github.com/devsy-org/devsy/issues/547)) ([6fe7c4c](https://github.com/devsy-org/devsy/commit/6fe7c4c68c492718e25dcb733e2ee6e1eb97044f))
* **deps:** update module github.com/loft-sh/agentapi/v4 to v4.7.1 ([#561](https://github.com/devsy-org/devsy/issues/561)) ([7413389](https://github.com/devsy-org/devsy/commit/7413389c9bed9cbb567225c4db055d9102914989))
* **deps:** update module github.com/loft-sh/agentapi/v4 to v4.8.1 ([#669](https://github.com/devsy-org/devsy/issues/669)) ([4a7835c](https://github.com/devsy-org/devsy/commit/4a7835c64bcf8396ad98f34fa80d17d051c4d934))
* **deps:** update module github.com/moby/buildkit to v0.28.0 ([#553](https://github.com/devsy-org/devsy/issues/553)) ([9a95f7f](https://github.com/devsy-org/devsy/commit/9a95f7fd6058b10009cca96a5f5155bdb66a72ec))
* **deps:** update module github.com/moby/buildkit to v0.28.1 ([#642](https://github.com/devsy-org/devsy/issues/642)) ([139d061](https://github.com/devsy-org/devsy/commit/139d0614c57bcbce7302bb8ec2a4a303142462e0))
* **deps:** update module github.com/moby/buildkit to v0.29.0 ([#689](https://github.com/devsy-org/devsy/issues/689)) ([c26fb2d](https://github.com/devsy-org/devsy/commit/c26fb2dc233173008f44610d4dd32c614b6aa2a7))
* **deps:** update module github.com/skevetter/ssh to v0.1.0 ([#596](https://github.com/devsy-org/devsy/issues/596)) ([9873496](https://github.com/devsy-org/devsy/commit/9873496256bcfe7edc84ae758332c48473604ac3))
* **deps:** update module github.com/tidwall/jsonc to v0.3.3 ([#634](https://github.com/devsy-org/devsy/issues/634)) ([09ef2e7](https://github.com/devsy-org/devsy/commit/09ef2e7df6ccbf945f9ce0c75f95fc127378f2e8))
* **deps:** update module golang.org/x/crypto to v0.48.0 ([#470](https://github.com/devsy-org/devsy/issues/470)) ([a496fa2](https://github.com/devsy-org/devsy/commit/a496fa29cce9c20e13b5b1863fcf4343b8b78aca))
* **deps:** update module golang.org/x/mod to v0.33.0 ([#472](https://github.com/devsy-org/devsy/issues/472)) ([62d77b8](https://github.com/devsy-org/devsy/commit/62d77b8bdd002de5871bcb597fc296f8418e33c9))
* **deps:** update module golang.org/x/sys to v0.42.0 ([#594](https://github.com/devsy-org/devsy/issues/594)) ([7dcbae7](https://github.com/devsy-org/devsy/commit/7dcbae7131ddf4396f679e9641672108cf546396))
* **deps:** update module google.golang.org/grpc to v1.79.0 ([#481](https://github.com/devsy-org/devsy/issues/481)) ([928cd45](https://github.com/devsy-org/devsy/commit/928cd4542f330ef0be44dcc19560cfee78d1a261))
* **deps:** update module google.golang.org/grpc to v1.79.1 ([#486](https://github.com/devsy-org/devsy/issues/486)) ([35199d2](https://github.com/devsy-org/devsy/commit/35199d2f279427394748cece0d8c9557b67887ab))
* **deps:** update module google.golang.org/grpc to v1.79.2 ([#579](https://github.com/devsy-org/devsy/issues/579)) ([b9d05dc](https://github.com/devsy-org/devsy/commit/b9d05dcedac61a0336e56297e2b8e9a7d4d634f3))
* **deps:** update module google.golang.org/grpc to v1.79.3 ([#635](https://github.com/devsy-org/devsy/issues/635)) ([951527a](https://github.com/devsy-org/devsy/commit/951527a4685c6fbb65e1df7f8885f5c540a02aa1))
* **deps:** update module google.golang.org/grpc to v1.80.0 ([#691](https://github.com/devsy-org/devsy/issues/691)) ([96a4ae9](https://github.com/devsy-org/devsy/commit/96a4ae9964172102b3982fd0db4d78045974dce0))
* **deps:** update module k8s.io/klog/v2 to v2.140.0 ([#581](https://github.com/devsy-org/devsy/issues/581)) ([e2c1d6c](https://github.com/devsy-org/devsy/commit/e2c1d6c32ed23ea8f54f0e9cf0e757fdf79e4cfa))
* **deps:** update module sigs.k8s.io/controller-runtime to v0.23.2 ([#567](https://github.com/devsy-org/devsy/issues/567)) ([ef70a92](https://github.com/devsy-org/devsy/commit/ef70a92dfd9976b6a4d3f25cebc91c9f0c79258e))
* **deps:** update module sigs.k8s.io/controller-runtime to v0.23.3 ([#568](https://github.com/devsy-org/devsy/issues/568)) ([3aeed65](https://github.com/devsy-org/devsy/commit/3aeed655ce7514fb8058cc101c4a9be10e59a883))
* **deps:** update module tailscale.com to v1.94.2 ([#502](https://github.com/devsy-org/devsy/issues/502)) ([273dc28](https://github.com/devsy-org/devsy/commit/273dc28cc1ee77a2a41b7a0c57dfefbef159a77f))
* **deps:** update module tailscale.com to v1.96.0 ([#582](https://github.com/devsy-org/devsy/issues/582)) ([ba577ae](https://github.com/devsy-org/devsy/commit/ba577aed2f8440d32349fb8d841e1bbbe2240f85))
* **deps:** update module tailscale.com to v1.96.5 ([#652](https://github.com/devsy-org/devsy/issues/652)) ([8ec0e80](https://github.com/devsy-org/devsy/commit/8ec0e804262f585ccbb418a81425d4508d660183))
* **deps:** update tanstack-query monorepo to v4.43.0 ([#484](https://github.com/devsy-org/devsy/issues/484)) ([2fd88b1](https://github.com/devsy-org/devsy/commit/2fd88b1380e5ef0fbe03b6b1fc131acc380195f8))
* **desktop:** strip newlines from environment variable ([#492](https://github.com/devsy-org/devsy/issues/492)) ([22ef145](https://github.com/devsy-org/devsy/commit/22ef145a9219b18f892374e5df217c2523dac5be))
* **desktop:** tray icon for flatpaks ([#659](https://github.com/devsy-org/devsy/issues/659)) ([20d4ddb](https://github.com/devsy-org/devsy/commit/20d4ddbd4b7bfeb30191b4437d176f8599c9ea16))
* **devcontainer/config:** parsing container environment variables during userenvprobe ([#497](https://github.com/devsy-org/devsy/issues/497)) ([4d79fa2](https://github.com/devsy-org/devsy/commit/4d79fa26092917f46ea453761a306c9712eed15e))
* **devcontainer/feature:** resolve _REMOTE_USER from merged metadata during feature install ([#496](https://github.com/devsy-org/devsy/issues/496)) ([2a5ff5c](https://github.com/devsy-org/devsy/commit/2a5ff5c4825e25aa8d6104d91ab6b3c197e6a707))
* **devcontainer/setup:** shell quoting for lifecycle commands ([#494](https://github.com/devsy-org/devsy/issues/494)) ([1ff0c8b](https://github.com/devsy-org/devsy/commit/1ff0c8b7c854586c74fde5b2b6254153c76f9936))
* **devcontainer/single:** postStartCommand skipped after container restart ([#660](https://github.com/devsy-org/devsy/issues/660)) ([0d0206c](https://github.com/devsy-org/devsy/commit/0d0206c1a2d16397c4ad9c6d74f519562c9a39b9))
* **devcontainer/sshtunnel:** eof handling in SSH execution and context cancellation ([#585](https://github.com/devsy-org/devsy/issues/585)) ([5938cea](https://github.com/devsy-org/devsy/commit/5938cea85aa45d43aa2e406bbabebad9c6a5004b))
* **devcontainer:** compose builds when context differs from devcontainer path ([#493](https://github.com/devsy-org/devsy/issues/493)) ([2a590dc](https://github.com/devsy-org/devsy/commit/2a590dc0c0c0a49a6e444791e07e96d3d0b447d2))
* **devcontainer:** compose mutation of shared local image tag ([#655](https://github.com/devsy-org/devsy/issues/655)) ([ba33e78](https://github.com/devsy-org/devsy/commit/ba33e78eef9f6f895bd99159154567b85c5e0767))
* **devcontainer:** include feature containerEnv in metadata label and warn on parse failure ([#231](https://github.com/devsy-org/devsy/issues/231)) ([ad731d4](https://github.com/devsy-org/devsy/commit/ad731d4a36e115564b39e8565dd0f0c282434b27))
* **devcontainer:** merge init environment variables to env ([#526](https://github.com/devsy-org/devsy/issues/526)) ([9230696](https://github.com/devsy-org/devsy/commit/9230696254525635a32b3ef60ec8a956007bd86d))
* **devcontainer:** strip digest from compose feature build image refs ([#459](https://github.com/devsy-org/devsy/issues/459)) ([d916613](https://github.com/devsy-org/devsy/commit/d916613795d72a480bdef499e26ce37a4588b76a))
* **docker:** detect GPU support via CDI for Podman runtimes ([#273](https://github.com/devsy-org/devsy/issues/273)) ([61d741d](https://github.com/devsy-org/devsy/commit/61d741df0e4117c6b2e644140f54198e431de3c4))
* **docker:** restart and wait for container readiness before exec ([#688](https://github.com/devsy-org/devsy/issues/688)) ([b3b2fe6](https://github.com/devsy-org/devsy/commit/b3b2fe69e5c4b102396e1982a95582be3e8ae04a))
* **docs:** rename devpod media files to devsy to fix broken images ([#4](https://github.com/devsy-org/devsy/issues/4)) ([a403e65](https://github.com/devsy-org/devsy/commit/a403e65a2e7b4cde53aa4eed2fc7dcecdbcdd225))
* **docs:** rename missed devpod doc files and fix broken anchor links ([#3](https://github.com/devsy-org/devsy/issues/3)) ([2a6026e](https://github.com/devsy-org/devsy/commit/2a6026e9464227fcdaa0e555546cbb5ada3fcde3))
* **driver/docker:** restore docker buildx build with fallback to buildkit ([#471](https://github.com/devsy-org/devsy/issues/471)) ([c24aa5d](https://github.com/devsy-org/devsy/commit/c24aa5d119a439c225a809588fbea9d567c5d7b1))
* **driver/kubernetes:** unable to pull credentials for registry ([#430](https://github.com/devsy-org/devsy/issues/430)) ([32a3c93](https://github.com/devsy-org/devsy/commit/32a3c93d03e247dbf2e7a022c7e5c2873e18f0a3))
* **e2e:** add comment noting devcontainer.json requirement for test repo ([#143](https://github.com/devsy-org/devsy/issues/143)) ([0f23b76](https://github.com/devsy-org/devsy/commit/0f23b761b807243d7dd3c3ebedee371a1d1d905e))
* **e2e:** add retry logic for transient workspace lookup in port-forward test ([#123](https://github.com/devsy-org/devsy/issues/123)) ([d30b09e](https://github.com/devsy-org/devsy/commit/d30b09ec947dd081f6d22b52551118a032d2cf4c))
* **e2e:** add SSH retry logic for flaky Windows WSL tests ([#115](https://github.com/devsy-org/devsy/issues/115)) ([d7c2ee4](https://github.com/devsy-org/devsy/commit/d7c2ee40d7705d4c6a8eff3517b82109e1f156a9))
* **e2e:** add SSH retry logic for flaky Windows WSL tests ([#99](https://github.com/devsy-org/devsy/issues/99)) ([c5389c5](https://github.com/devsy-org/devsy/commit/c5389c5f7040d46ee7693d049d4933a6a467905e))
* **e2e:** correct runServices error assertion ([#224](https://github.com/devsy-org/devsy/issues/224)) ([4465aa9](https://github.com/devsy-org/devsy/commit/4465aa99abda57bb63e20b5ca50384a290ab57f6))
* **e2e:** increase machineprovider2 inactivity timeout from 5s to 30s ([#140](https://github.com/devsy-org/devsy/issues/140)) ([1a02cde](https://github.com/devsy-org/devsy/commit/1a02cde879f566e1d54c1d5a1167b08eef51dacb))
* **e2e:** increase timeout for container-pull-heavy E2E tests ([#222](https://github.com/devsy-org/devsy/issues/222)) ([c9e438d](https://github.com/devsy-org/devsy/commit/c9e438d1a60b5e49211953add39a572176b2750b))
* **e2e:** increase TimeoutShort from 2min to 3min ([#212](https://github.com/devsy-org/devsy/issues/212)) ([212e381](https://github.com/devsy-org/devsy/commit/212e3818d2d616d3819da132acbaa72a7e6ab33f))
* **e2e:** migrate all test images to ghcr.io/devsy-org registry ([#146](https://github.com/devsy-org/devsy/issues/146)) ([9fce990](https://github.com/devsy-org/devsy/commit/9fce990dd44e7d2dc47fdb58ecffdaa1e75dcafd))
* **e2e:** replace external devcontainer image with own registry test container ([#223](https://github.com/devsy-org/devsy/issues/223)) ([b4b1afa](https://github.com/devsy-org/devsy/commit/b4b1afac8e1441aa12ed6e705b2318cf2befe8a3))
* **e2e:** replace MCR image reference with GHCR test image ([#125](https://github.com/devsy-org/devsy/issues/125)) ([ea4f1ed](https://github.com/devsy-org/devsy/commit/ea4f1edcf69ad6708939fa11231c23318a6ac219))
* **e2e:** replace mcr.microsoft.com references with ghcr.io/devsy-org test images ([#241](https://github.com/devsy-org/devsy/issues/241)) ([143b86e](https://github.com/devsy-org/devsy/commit/143b86eba082786b4ee5bfd714716a9411e76927))
* **exec:** use workspace UID for container lookup ([#160](https://github.com/devsy-org/devsy/issues/160)) ([b2af297](https://github.com/devsy-org/devsy/commit/b2af29745fc19322379d0a8b29794da2091a7ce8))
* extend timeout when getting Docker credentials from host ([#575](https://github.com/devsy-org/devsy/issues/575)) ([50c203f](https://github.com/devsy-org/devsy/commit/50c203f9f827c552d29f02aa6c605b176316928e))
* **extract:** add path traversal guard for tar extraction ([#92](https://github.com/devsy-org/devsy/issues/92)) ([ac70e7b](https://github.com/devsy-org/devsy/commit/ac70e7bd030ca62d3d8df62f2dda456c1ad74004))
* **feature:** add SHA-256 integrity verification for direct tar downloads ([#89](https://github.com/devsy-org/devsy/issues/89)) ([ae95ec8](https://github.com/devsy-org/devsy/commit/ae95ec8e029504ef4b6fefcdce92c0600df4af9d))
* **feature:** prevent false positive circular dependency detection for installsAfter ([#240](https://github.com/devsy-org/devsy/issues/240)) ([03e5cb1](https://github.com/devsy-org/devsy/commit/03e5cb149298c84c70d021a365f45e3d37b9bc0c))
* **feature:** resolve lint failures in OCI annotations code ([#221](https://github.com/devsy-org/devsy/issues/221)) ([c368e1e](https://github.com/devsy-org/devsy/commit/c368e1eefa16d3351760ef3884f5b8b125786083))
* **features:** enforce dependsOn constraints in overrideFeatureInstallOrder ([#151](https://github.com/devsy-org/devsy/issues/151)) ([7da82b8](https://github.com/devsy-org/devsy/commit/7da82b838ad75e6b1b44e5a566303ac1203041a0))
* **flatpak:** grant xdg-run permissions to podman ([#450](https://github.com/devsy-org/devsy/issues/450)) ([40b723d](https://github.com/devsy-org/devsy/commit/40b723dcdc8d7ae07e2d108537c9cfb1d29396c0))
* **graph:** use round-based topological sort in sortNodeIDsWithPriority ([#235](https://github.com/devsy-org/devsy/issues/235)) ([b3d0008](https://github.com/devsy-org/devsy/commit/b3d00083e78b58995c6758ed57dce5589ee7b3a2))
* **ide/vscode:** extension installation in VS Code flavors ([#636](https://github.com/devsy-org/devsy/issues/636)) ([dca065b](https://github.com/devsy-org/devsy/commit/dca065b814048ead5c3788f6b0bd12dea2ec14b3))
* **image:** sanitize HTML error pages in container registry responses ([#127](https://github.com/devsy-org/devsy/issues/127)) ([021184c](https://github.com/devsy-org/devsy/commit/021184cc8d08cac5366f5747be6afd8ec0d8b859))
* **images:** resize cursor icon ([#650](https://github.com/devsy-org/devsy/issues/650)) ([5e92a90](https://github.com/devsy-org/devsy/commit/5e92a901a8b62b04099be657d76251892d0008e7))
* **inject:** chmod binary before mv to prevent entrypoint race on Alpine ([#264](https://github.com/devsy-org/devsy/issues/264)) ([594f71c](https://github.com/devsy-org/devsy/commit/594f71ca4d571e6442958099b27bad61ceeb7f02))
* **inject:** resolve goroutine race in pipe() bidirectional copy ([#139](https://github.com/devsy-org/devsy/issues/139)) ([51a9405](https://github.com/devsy-org/devsy/commit/51a9405d7a4a542ba567a7b0989e7d853b501dd1))
* **lifecycle:** accept initializeCommand as a valid waitFor value ([#114](https://github.com/devsy-org/devsy/issues/114)) ([908300a](https://github.com/devsy-org/devsy/commit/908300a68ff15d9e14b278fee9d8db88bc33cb37))
* **lifecycle:** accept initializeCommand as a valid waitFor value ([#98](https://github.com/devsy-org/devsy/issues/98)) ([4953b11](https://github.com/devsy-org/devsy/commit/4953b1132b163cb28094a9288b5628112ad62c53))
* **lifecycle:** enforce waitFor property in lifecycle hook execution ([#87](https://github.com/devsy-org/devsy/issues/87)) ([f366335](https://github.com/devsy-org/devsy/commit/f366335818c36b684b1df1695bedb9715c6e39f2))
* **lifecycle:** install dotfiles between postCreate and postStart ([#97](https://github.com/devsy-org/devsy/issues/97)) ([7b67e90](https://github.com/devsy-org/devsy/commit/7b67e9099f0eeaabae108062fe13f017e63efed7))
* **lifecycle:** install dotfiles between postCreate and postStart ([#97](https://github.com/devsy-org/devsy/issues/97)) ([#112](https://github.com/devsy-org/devsy/issues/112)) ([fe8884b](https://github.com/devsy-org/devsy/commit/fe8884b16d0f37c70a37898c5924a7dd06ce0533))
* **lifecycle:** resolve dotfiles race condition in deferred hooks ([#113](https://github.com/devsy-org/devsy/issues/113)) ([f72417d](https://github.com/devsy-org/devsy/commit/f72417dc957a9c9b75196b7ff7f397afe9bd714b))
* **lifecycle:** run initializeCommand named sub-commands in parallel ([#100](https://github.com/devsy-org/devsy/issues/100)) ([a5e7e27](https://github.com/devsy-org/devsy/commit/a5e7e27b5c0927ab9cfb02fb5c2e8d2cd21e0976))
* **lifecycle:** run initializeCommand named sub-commands in parallel ([#116](https://github.com/devsy-org/devsy/issues/116)) ([93f8171](https://github.com/devsy-org/devsy/commit/93f81715a181f3da24d8a1bd234d3cde63b8a7fa))
* **lifecycle:** run named sub-commands in parallel within lifecycle hooks ([#95](https://github.com/devsy-org/devsy/issues/95)) ([bc4be89](https://github.com/devsy-org/devsy/commit/bc4be89178cff178066fd7ef34fa60fb3bbd9c41))
* **lifecycle:** run postAttachCommand on every attach per spec ([#177](https://github.com/devsy-org/devsy/issues/177)) ([83d024c](https://github.com/devsy-org/devsy/commit/83d024cb6656463297225106dd64fb9c9187eac1))
* **lifecycle:** surface deferred hook errors instead of swallowing them ([#189](https://github.com/devsy-org/devsy/issues/189)) ([b7fbfc9](https://github.com/devsy-org/devsy/commit/b7fbfc9a06e45adf34ada3d2df3b90538d58f09f))
* **lifecycle:** warn when waitFor references phase with no commands ([#120](https://github.com/devsy-org/devsy/issues/120)) ([60f5b1a](https://github.com/devsy-org/devsy/commit/60f5b1a8dcc78b9a35171741ff97ad8822531ab9))
* **lint:** remove nolint:gosec directives from templates and e2e tests ([#225](https://github.com/devsy-org/devsy/issues/225)) ([58d2ba8](https://github.com/devsy-org/devsy/commit/58d2ba8012099102a00720b1686a61c675c1a9b2))
* **log:** bridge klog to zap backend via LogrSink ([#299](https://github.com/devsy-org/devsy/issues/299)) ([9b0073b](https://github.com/devsy-org/devsy/commit/9b0073b4a6bc6aca165e192ab4e162b7fce12fd7))
* **log:** make global logger thread-safe with atomic.Pointer ([#155](https://github.com/devsy-org/devsy/issues/155)) ([7fdb0f4](https://github.com/devsy-org/devsy/commit/7fdb0f4480591584542f2f28a6279f361dd9f81f))
* **netstat:** handle missing /proc/net/tcp6 on IPv6-disabled hosts ([#706](https://github.com/devsy-org/devsy/issues/706)) ([cb27dd4](https://github.com/devsy-org/devsy/commit/cb27dd469607f3252bac2b47a16e3cf6f413c641))
* prevent stdout corruption in kubernetes e2e tests ([#24](https://github.com/devsy-org/devsy/issues/24)) ([ed3e252](https://github.com/devsy-org/devsy/commit/ed3e252aac936a1f8c5620d664858df0b6608c0a))
* propagate GitSSHSigningKey in daemon path and thread context through backhaul ([#722](https://github.com/devsy-org/devsy/issues/722)) ([d543be9](https://github.com/devsy-org/devsy/commit/d543be92b35f2304a2d8efd4ca425a437b8f17c3))
* rename flatpak files from DevPod/loft to Devsy ([#9](https://github.com/devsy-org/devsy/issues/9)) ([a884e7f](https://github.com/devsy-org/devsy/commit/a884e7f9bcc9ea10b59ff06fd8678124ddcbd834))
* rename logo images and fix broken badge URL in README ([#10](https://github.com/devsy-org/devsy/issues/10)) ([58a85f8](https://github.com/devsy-org/devsy/commit/58a85f8a0ccdcf13301428e3ddca24903060325b))
* resolve Git SSH signature forwarding failures ([#704](https://github.com/devsy-org/devsy/issues/704)) ([3cc1d02](https://github.com/devsy-org/devsy/commit/3cc1d02a1e4634d354f155ab5c3e8163257fd0b6))
* resolve SSH signature key path for container-to-host forwarding ([#714](https://github.com/devsy-org/devsy/issues/714)) ([696b944](https://github.com/devsy-org/devsy/commit/696b94402909d9ecab305801dcb45069e05c0a75))
* resolve unused rust warnings and spelling issues ([#700](https://github.com/devsy-org/devsy/issues/700)) ([45249fc](https://github.com/devsy-org/devsy/commit/45249fc8b3da3e57af0f36d8697818cf8ce84dbf))
* **run:** respect user-specified consistency in workspaceMount ([#182](https://github.com/devsy-org/devsy/issues/182)) ([d7bb5ed](https://github.com/devsy-org/devsy/commit/d7bb5edd8a30dea117fcccd317713a5117ba4483))
* sanitize AppImage environment before opening URLs ([#710](https://github.com/devsy-org/devsy/issues/710)) ([702c227](https://github.com/devsy-org/devsy/commit/702c227848d219735db7cf9ae136ca17488e6880))
* **shell:** update module mvdan.cc/sh/v3 to v3.13.1 ([#692](https://github.com/devsy-org/devsy/issues/692)) ([8f8e5cd](https://github.com/devsy-org/devsy/commit/8f8e5cd7653850cb4169e749f3358eb6f22f687b))
* **ssh/agent:** expand tilde in SSH_AUTH_SOCK path ([#674](https://github.com/devsy-org/devsy/issues/674)) ([4fb2d7b](https://github.com/devsy-org/devsy/commit/4fb2d7b962a6ab5f3a9d7505df034cd53dcd3fc4)), closes [#671](https://github.com/devsy-org/devsy/issues/671)
* **ssh/server:** disable PTY emulation to prevent terminal rendering issues in TUI programs ([#595](https://github.com/devsy-org/devsy/issues/595)) ([a26eb64](https://github.com/devsy-org/devsy/commit/a26eb646002223dc7ee040d5e58b5bae8d644ebe))
* **ssh/server:** pseudo-tty signal handling ([#601](https://github.com/devsy-org/devsy/issues/601)) ([273ed5b](https://github.com/devsy-org/devsy/commit/273ed5b64e0e5db629ea0cdae563d323963ec3bb))
* **ssh/server:** start PTY with the client terminal dimensions ([#583](https://github.com/devsy-org/devsy/issues/583)) ([f833edb](https://github.com/devsy-org/devsy/commit/f833edb85ada0a66e94c20b4398270fb6af8fba1))
* **ssh:** skip file path signing keys in GPG agent forwarding ([#734](https://github.com/devsy-org/devsy/issues/734)) ([8f408e4](https://github.com/devsy-org/devsy/commit/8f408e42dbc492f0a0b64db24842ddf63132d0af))
* **ssh:** use platform raw terminal handling for PTY sessions ([#698](https://github.com/devsy-org/devsy/issues/698)) ([ad7254f](https://github.com/devsy-org/devsy/commit/ad7254fd08eb2321956123cb811743294fbb69a4))
* **test:** isolate GPG signing key unit tests from host git config ([#233](https://github.com/devsy-org/devsy/issues/233)) ([c6034f3](https://github.com/devsy-org/devsy/commit/c6034f32180a5dac8bc3b1d3a1126f96dcc593cc))
* **tunnel:** do not error when user exits ssh connection ([#586](https://github.com/devsy-org/devsy/issues/586)) ([371e52e](https://github.com/devsy-org/devsy/commit/371e52e2bdbfc97eb1dc2fffb2d04085b7bbeab0))
* **tunnel:** extract JSON log lines instead of double-wrapping them ([#141](https://github.com/devsy-org/devsy/issues/141)) ([43ceb3f](https://github.com/devsy-org/devsy/commit/43ceb3ffe3b84ffa15b01948bcc8411b51096767))
* **tunnel:** PipeBridge shutdown and goroutine leak fixes ([#158](https://github.com/devsy-org/devsy/issues/158)) ([b91e15a](https://github.com/devsy-org/devsy/commit/b91e15aedc81fee0da18e94a0ce5cf1060512c51))
* **tunnel:** resolve SSH handshake EOF race in container tunnel ([#86](https://github.com/devsy-org/devsy/issues/86)) ([81d5811](https://github.com/devsy-org/devsy/commit/81d5811eaa77c6b6ae09f6f57e681362ccf71cc4))
* **ui:** add error handling to provider options form ([#625](https://github.com/devsy-org/devsy/issues/625)) ([167febf](https://github.com/devsy-org/devsy/commit/167febff3474c5d9de3bf20b6bddf1011aca8248))
* **ui:** migrate to Tauri opener plugin for file downloads from app ([#627](https://github.com/devsy-org/devsy/issues/627)) ([ab54f8a](https://github.com/devsy-org/devsy/commit/ab54f8afb4f6d97e170ce2d10cb74e47a097293b))
* **ui:** provider modal view issues when adding new providers ([#646](https://github.com/devsy-org/devsy/issues/646)) ([c005899](https://github.com/devsy-org/devsy/commit/c00589940a5063f34f25e6de1e17c8cb42a343e3))
* **upgrade:** check if already up-to-date ([#559](https://github.com/devsy-org/devsy/issues/559)) ([ae7d61e](https://github.com/devsy-org/devsy/commit/ae7d61e7a83ada613e0b90176f33b8b6d7fb0b84))
* **upgrade:** install correct binary release for operating system during selfupdate ([#535](https://github.com/devsy-org/devsy/issues/535)) ([62e38b8](https://github.com/devsy-org/devsy/commit/62e38b81cef998fcdecb0a8848cc53a8113064ac))
* **upgrade:** trim 'v' prefix from current version check in Upgrade function ([#574](https://github.com/devsy-org/devsy/issues/574)) ([f18b6e2](https://github.com/devsy-org/devsy/commit/f18b6e27c72f84d5a54b4090f5254c241c047e23))
* use docker-credentials endpoint for helper auth flow ([#478](https://github.com/devsy-org/devsy/issues/478)) ([6f54ce2](https://github.com/devsy-org/devsy/commit/6f54ce226faa1e7489bae17de4a84661e1fa94b5))
* **ux:** add operation context to error messages at command boundary ([#133](https://github.com/devsy-org/devsy/issues/133)) ([8c6740f](https://github.com/devsy-org/devsy/commit/8c6740f42ed6a7c48aa7b512af9c52ee2155d8bb))
* **ux:** include lifecycle phase name in hook error messages ([#132](https://github.com/devsy-org/devsy/issues/132)) ([bcdb2e0](https://github.com/devsy-org/devsy/commit/bcdb2e00947f3c1d7a7182ce046e3f37e879b7e8))
* **ux:** prevent remote agent fatal log from killing local CLI ([#130](https://github.com/devsy-org/devsy/issues/130)) ([37a153e](https://github.com/devsy-org/devsy/commit/37a153e6ade10637b81be302c2a63995d33ed775))
* **ux:** print error message on SSH/exec exit failures ([#128](https://github.com/devsy-org/devsy/issues/128)) ([61c84c9](https://github.com/devsy-org/devsy/commit/61c84c9e98da12e28bfe5aaa778e4992bc1797e2))
* **ux:** propagate swallowed errors in workspace resolution ([#131](https://github.com/devsy-org/devsy/issues/131)) ([41d7d38](https://github.com/devsy-org/devsy/commit/41d7d385e08275861536374ba9c07744b215f655))
* **ux:** remove duplicate error logging in feature downloads ([#135](https://github.com/devsy-org/devsy/issues/135)) ([bf5a96f](https://github.com/devsy-org/devsy/commit/bf5a96f446accb072ba8be31203a6eacfb74f40b))
* **ux:** replace %w with %v in log format strings ([#129](https://github.com/devsy-org/devsy/issues/129)) ([7697411](https://github.com/devsy-org/devsy/commit/7697411984d68c60bb4c28ca6dc141bef60a3ced))
* **ux:** use %w for proper error chain wrapping ([#134](https://github.com/devsy-org/devsy/issues/134)) ([1360313](https://github.com/devsy-org/devsy/commit/1360313f4136917c1dacd11941fb8a0237d0d9b9))
* **workspace:** force-remove workspace folder with restrictive permissions ([#297](https://github.com/devsy-org/devsy/issues/297)) ([f8f41f6](https://github.com/devsy-org/devsy/commit/f8f41f6baf2e82e819b3acdf0390eb7d17ed0c99))
* **workspace:** propagate findWorkspace errors and fix list append bug ([#124](https://github.com/devsy-org/devsy/issues/124)) ([81ab5f1](https://github.com/devsy-org/devsy/commit/81ab5f1d0170b4425723c0828d62859208a681a2))


### Reverts

* undo commits after 37018553b (PRs [#90](https://github.com/devsy-org/devsy/issues/90), [#91](https://github.com/devsy-org/devsy/issues/91), [#97](https://github.com/devsy-org/devsy/issues/97)-101) ([#111](https://github.com/devsy-org/devsy/issues/111)) ([ec11fbd](https://github.com/devsy-org/devsy/commit/ec11fbdf04137657eda22e8fc25bed39b0131ec2))

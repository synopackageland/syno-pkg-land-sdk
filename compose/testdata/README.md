# Compose fixtures and licenses

Immich and OpenList bytes are preserved from upstream. These files retain the
upstream repositories' AGPL-3.0 license, separately from the MIT SDK code. See
[NOTICE.txt](NOTICE.txt) for attribution, redistribution notices and scope, and
the verbatim license copies for [Immich](licenses/immich-AGPL-3.0.txt) and
[OpenList](licenses/openlist-AGPL-3.0.txt). The YAML/env files themselves are the
redistributed source; keep their notice and license copies with them.

`vaultwarden.yaml` is independently authored SDK test data under the root
[MIT license](../../LICENSE), copyright (c) 2026 synopkgland. On 2026-10-18 it
replaced the wiki excerpt whose license could not be established. It keeps the
same service/image, restart policy, signup environment, writable data bind and
TCP port mapping, using explicit Compose syntax and no wiki prose/comments.
`TestVaultwardenFixtureRuntimeContract` checks that contract and SDK planning.

| File | License | SHA256 | Source |
| --- | --- | --- | --- |
| `openlist.yaml` | AGPL-3.0 | `8c452afe9b544369eb11f70d7eeab72f14475912f42571646bbde6086ad1ba52` | https://github.com/OpenListTeam/OpenList/blob/3b28270a4066ea52943a69ecddd850d83f7e8bdc/docker-compose.yml |
| `immich.yaml` | AGPL-3.0 | `2601c893aa3217d3c0c7284c23aaa1699aac75b533c84c0315fd16b7889fd552` | https://github.com/immich-app/immich/releases/download/v3.3.3/docker-compose.yml |
| `immich.env` | AGPL-3.0 | `dda820cecbcf5fd5d6afecc54107bbd3c973088c631d380dfc83e9f0f2c6b210` | https://github.com/immich-app/immich/releases/download/v3.3.3/example.env |
| `vaultwarden.yaml` | MIT | `da4f47f6e9e4954b2b5905258daea6e0fa5fc82148a64bba1921db7869612d8a` | SDK-authored fixture in this directory |

Container images are not redistributed here; their licenses are separate from
these fixture files. The application images are unmodified and locked by the
SDK registry resolver at build time. The Immich env contains the public example
password, not a user credential. These fixture notices are separate from the
planner Go dependency notices in [../THIRD_PARTY_NOTICES.txt](../THIRD_PARTY_NOTICES.txt).

Planner unit tests use all three application fixtures. The CLI real-container e2e uses Vaultwarden and Immich; see scripts/ci/compose-e2e.mjs and app-protocol-e2e.py in the CLI repository. These are Linux fixture proofs, not DSM acceptance or per-user NAS ACL evidence.

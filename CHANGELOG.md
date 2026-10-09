# Changelog

## 1.0.0 (2026-10-09)


### Features

* add cancel action and private vote confirmations ([168f9e8](https://github.com/laluowen/ChipIn/commit/168f9e821210a7679b31c3be52a5d4c2f489a7e0))
* add Cloud Run webhook mode with Firestore storage ([ba054b1](https://github.com/laluowen/ChipIn/commit/ba054b155738b7cbae3168a5167016eb2550dac3))
* allow retracting a vote ([8d82f5d](https://github.com/laluowen/ChipIn/commit/8d82f5dbf2b8405b6f244bbe69eef6e0526618ce))
* derive vote scale and consensus from Linear team settings ([b609807](https://github.com/laluowen/ChipIn/commit/b6098072db26572cb4396179bb4e9dd047c277c6))
* implement Mode A planning poker (Socket Mode, SQLite, Linear) ([eb9a18d](https://github.com/laluowen/ChipIn/commit/eb9a18d177555312e4fcc1c1dcdbb340ad0ca42a))


### Bug Fixes

* avoid Firestore lookup on estimate_select interaction ([4ccd4a1](https://github.com/laluowen/ChipIn/commit/4ccd4a1fac536d6f96bae4225c1100dde2a5a5f6))
* pass package path to go build in Dockerfile ([22476c6](https://github.com/laluowen/ChipIn/commit/22476c690754e8930a7d60cbc32d6441995e8fe0))
* resolve golangci-lint findings across the codebase ([7532386](https://github.com/laluowen/ChipIn/commit/7532386fe7654a6123f76a38df68c44f718af31f))
* use localhost in firestore.sh, not 127.0.0.1 ([6479ec3](https://github.com/laluowen/ChipIn/commit/6479ec3d24f2d21525c2777a14a25902885a0dc0))

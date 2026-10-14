# Changelog

## [1.0.2](https://github.com/laluowen/ChipIn/compare/v1.0.1...v1.0.2) (2026-10-14)


### Bug Fixes

* **deps:** update module github.com/slack-go/slack to v0.30.1 ([515f7c3](https://github.com/laluowen/ChipIn/commit/515f7c31a29f13119607a743578e605d41e1df11))
* **deps:** update module google.golang.org/grpc to v1.84.0 ([d8f1cd3](https://github.com/laluowen/ChipIn/commit/d8f1cd39fae23f979164ead74e22b0c9fd162d63))
* **deps:** update module google.golang.org/grpc to v1.84.0 ([3f20f7b](https://github.com/laluowen/ChipIn/commit/3f20f7bf127f440adb466795fc2819e25dd418ba))
* **deps:** update module modernc.org/sqlite to v1.60.1 ([4b9de45](https://github.com/laluowen/ChipIn/commit/4b9de4500bf032901293df176859ef3233173e31))

## [1.0.1](https://github.com/laluowen/ChipIn/compare/v1.0.0...v1.0.1) (2026-10-09)


### Bug Fixes

* upload raw binaries instead of tar.gz/zip archives ([e0b9331](https://github.com/laluowen/ChipIn/commit/e0b93318322e013513d5bf3932023c07966eccf5))

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

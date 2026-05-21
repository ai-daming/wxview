# Changelog

All notable changes to Wxview are documented here.

## 0.3.1 - 2026-05-21

### Changed

- WebAPI `--addr` now accepts explicit non-loopback listen addresses such as
  `0.0.0.0:38765` and LAN IP addresses.
- WebAPI startup and status output now reports `api exposure: loopback|network`.
- `/health` and `/v1/me` server metadata now include `exposure`.
- Health and identity self-checks use a loopback client address when the server
  listens on wildcard addresses such as `0.0.0.0` or `[::]`.

### Documentation

- Updated CLI help, README, and agent notes to describe default loopback
  behavior and explicit LAN listening.
- Documented that non-loopback WebAPI listeners are unauthenticated and should
  only be used on trusted networks.

## 0.3.0 - 2026-05-21

### Added

- Added the local WebAPI command group:
  `wxview api start/status/stop/serve`.
- Added account identity endpoints and metadata so callers can tell which local
  WeChat account a WebAPI instance is reading.
- Added full read-only WebAPI data endpoints for contacts, members, sessions,
  unread chats, messages, search, timeline, new-message preview, favorites,
  articles, and SNS data.
- Added `POST /v1/cache/refresh`, `POST /v1/new-messages/poll`, and
  `POST /v1/new-messages/reset` as the state-changing WebAPI actions.
- Added `GET /v1/media?path=...` for serving resolved local media files.
- Added shared `internal/query` use-case orchestration so CLI and WebAPI use the
  same query logic while keeping separate output formats.
- Added richer message `content_detail` parsing for images, videos, files,
  voice, quoted messages, forwarded chat records, covers, emoji/stickers,
  cards, locations, system events, transfers, and red envelopes.
- Added `wxview --version` and `wxview version`.

### Fixed

- Fixed forwarded chat record parsing.
- Exposed quoted message details so quote messages show what was referenced.
- Avoided weak video media matches that could make different video messages
  point to the same local file.
- Preserved video thumbnails when the full video file is not present locally.
- Fixed broad timeline queries so they keep using the global message index
  instead of falling back to slow scans.
- Parsed revoke system messages into structured content details.

### Notes

- WebAPI `GET` endpoints are query-only and ignore undeclared query parameters,
  including `refresh=true`.
- Media service only serves local files already present under the current
  account's WeChat data directory or Wxview cache; it does not download WeChat
  CDN media.

## 0.2.0 - 2026-05-19

### Added

- Added first-time setup with `wxview init`.
- Added background cache maintenance with `wxview daemon start`.
- Added derived message index maintenance for faster `messages` and `timeline`.
- Added CLI queries for contacts, members, sessions, unread chats,
  new-messages, messages, search, timeline, favorites, articles, and SNS.
- Added machine-readable `json`, `jsonl`, and `csv` outputs.
- Added local image, video, file, and voice media resolution when supporting
  caches are available.

### Notes

- Supports macOS and Windows WeChat 4.x local data layouts.
- Does not support Linux, macOS WeChat 3.x, WAL patching, or public hosted
  WebAPI deployment.

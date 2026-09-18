# Speccy

Speccy reviews markdown spec bundles and returns one verdict: Build Ready or Not Build Ready.

Speccy is at milestone M1. The binary serves the web app in local mode, and the store runs on SQLite and Postgres. The review does not exist yet.

## Quick start

You need Go, Node 24 or later, and `just`. `just test-pg` and `just verify` also need Docker.

```sh
git clone https://github.com/alternayte/speccy && cd speccy
just build
./bin/speccy
```

`speccy` starts local mode and opens your browser. Add `--no-open` to stop the browser from opening.

To work on Speccy, run `just dev` and open http://127.0.0.1:5173.

## Guarantees

This table lists only the guarantees whose tests pass today.

| Guarantee | Test |
|---|---|
| Both store engines pass the same conformance suite. | [`TestStoreConformance`](internal/store/conformance/conformance_test.go) |
| A concurrent append with a stale version is rejected. | [`TestEventStore_ConcurrentAppendRejected`](internal/es/es_test.go) |
| Projections update in the same transaction as the append. | [`TestEventStore_InlineProjectionAtomic`](internal/es/es_test.go) |
| Local mode refuses a non-loopback address. | [`TestLocalMode_LoopbackOnly`](internal/http/server_test.go) |

## How the verdict works

The review pipeline does not exist yet. This section describes it when milestone M3 is done.

## Modes

| Mode | Command | Status |
|---|---|---|
| Local | `speccy` | Serves the web app on 127.0.0.1. |
| Hosted | `speccy serve --hosted` | Not built yet. |
| Headless | `speccy review <path>` | Not built yet. |

## Try it on your existing specs

`speccy review docs/ --summary` does not exist yet. This section shows the command and a `.speccy.yaml` example when it exists.

## GitHub Action

The GitHub Action does not exist yet.

## Configuration

`docs/configuration.md` does not exist yet. Local mode takes one flag: `--addr`, default `127.0.0.1:7878`. The address must be a loopback address.

## Licence

AGPL-3.0. See [LICENSE](LICENSE).

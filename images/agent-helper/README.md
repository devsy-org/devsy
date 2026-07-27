# agent-helper image

## Build locally

Requires [Zig](https://ziglang.org) 0.16.0.

```sh
go run ./hack/agent_helper_image   # cross-compiles dist/helper-{amd64,arm64}
docker buildx build --platform linux/arm64 --load -t agent-helper:dev images/agent-helper
```

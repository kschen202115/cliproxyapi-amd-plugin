# CLIProxyAPI AMD Radeon Cloud plugin

This native CLIProxyAPI plugin registers an `amd` provider for Radeon Cloud's shared model API. It supports:

- API-key credentials (`rc-...`)
- Runtime model discovery from `GET /v1/models`
- OpenAI Chat Completions requests and SSE streaming through `POST /v1/chat/completions`
- Per-credential quota display from Radeon `GET /api/profile/model-usage`

The upstream base URL is fixed to `https://developer.amd.com.cn/radeon/api/v1`, per the Radeon Cloud documentation. The plugin makes all upstream requests through CLIProxyAPI's host HTTP bridge, so the host retains its configured proxy and request logging behavior.

## Build with GitHub Actions for a VPS

Push this directory to a GitHub repository, then use **Actions → Build CLIProxyAPI AMD plugin → Run workflow**. The workflow in [`.github/workflows/build.yml`](.github/workflows/build.yml) compiles and uploads these artifacts:

- `amd-linux-amd64` for a standard x86_64 VPS
- `amd-linux-arm64` for an ARM64 VPS

Download the artifact matching `uname -m` on the VPS (`x86_64` → `amd64`; `aarch64`/`arm64` → `arm64`). It contains a file named `amd.so`.

For CLIProxyAPI's **GitHub plugin-store installation**, push a version tag such as `v0.1.0`. The same workflow then creates a GitHub Release containing the precisely named archives and `checksums.txt` expected by the installer. Before publishing, replace `YOUR_GITHUB_OWNER/YOUR_GITHUB_REPOSITORY` in [registry.example.json](registry.example.json), commit it as `registry.json` on the default branch, and add its raw GitHub URL to `plugins.store-sources` on the VPS:

```yaml
plugins:
  enabled: true
  store-sources:
    - https://raw.githubusercontent.com/YOUR_GITHUB_OWNER/YOUR_GITHUB_REPOSITORY/main/registry.json
```

After the store refreshes, install the `amd` plugin from CLIProxyAPI's plugin UI/API. It selects the release asset matching the VPS architecture and installs it under the correct `plugins/linux/<arch>/` directory automatically.

CLIProxyAPI loads native dynamic libraries, so use the Linux `.so` artifact on a VPS. The checked-in `amd.dylib` is only a local macOS arm64 build and should not be copied to Linux.

If GitHub Actions is unavailable, build on the same OS/architecture as the CLIProxyAPI executable. CGO must be available:

```sh
# Linux VPS
CGO_ENABLED=1 go build -buildmode=c-shared -o amd.so .
```

The matching generated C header (`amd.h`) does not need to be installed. CLIProxyAPI discovers `amd.so` by filename, so the plugin ID is `amd`.

## Install and configure

1. If you use GitHub plugin-store installation, the installer creates the right path automatically. For a manual artifact installation, create the correct platform directory under the CLIProxyAPI data/config directory:

   ```sh
   # x86_64 VPS
   install -Dm755 amd.so plugins/linux/amd64/amd.so

   # ARM64 VPS
   install -Dm755 amd.so plugins/linux/arm64/amd.so
   ```

2. Enable the global and per-plugin switches shown in [examples/config.yaml](examples/config.yaml).
3. Place one or more AMD credential JSON files in CLIProxyAPI's auth directory. A ready-to-copy shape is in [examples/amd.json](examples/amd.json). Replace the placeholder with a Radeon Cloud API key.
4. Reload or restart CLIProxyAPI. The plugin calls Radeon `/v1/models` for each `amd` credential and exposes the returned model IDs.

Credential files are deliberately explicit (`provider: "amd"`). This prevents the auth parser from claiming unrelated JSON credentials in the same directory.

## Notes

- Radeon Cloud's model catalog is dynamic, so this plugin does not hard-code a model list.
- The management UI shows daily remaining/used/limit in USD, the requests-per-minute limit, and today's request/token/error counters. It also shows a daily-spend bucket that resets at Radeon `daily_reset_at`.
- Radeon reports `not_available` usage status as unknown, not as a misleading zero balance; the plugin surfaces that condition as an error.
- The Radeon shared endpoint accepts OpenAI-compatible Chat Completions, but not the unsupported parameters Radeon filters at its gateway. Use only parameters supported by the selected Radeon model.
- `executor.count_tokens` is intentionally unsupported: Radeon documents no shared Chat Completions token-count endpoint.
- This plugin uses the current CLIProxyAPI native plugin ABI (ABI v1, schema v6). Confirm the server exposes `X-CPA-SUPPORT-PLUGIN: 1` before installing.

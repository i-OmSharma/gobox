# veil

> A container runtime built from scratch in pure Go — no Docker, no containerd, no daemon.

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![Platform](https://img.shields.io/badge/platform-Linux-informational?style=flat&logo=linux)](https://kernel.org/)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat)](LICENSE)
[![Kernel](https://img.shields.io/badge/kernel-5.x%2B%20%28cgroups%20v2%29-blueviolet?style=flat)](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html)

veil speaks directly to Linux primitives — namespaces, cgroups, OverlayFS — to run OCI-compatible containers without any middleware.

---

## Features

| Primitive | What veil does |
|-----------|----------------|
| **Namespaces** | PID, UTS, Mount, IPC, Network isolation per container |
| **cgroups v2** | Hard memory and CPU limits, OOM-kill group |
| **OverlayFS** | Copy-on-write layer — image rootfs is never modified |
| **OCI images** | Pull from any OCI-compliant registry (Docker Hub, GHCR, etc.) |
| **veth + bridge** | Full internet access via NAT, per-container network namespace |
| **Port forwarding** | iptables DNAT rules wired before container starts |
| **Volume mounts** | Bind-mount host paths into the container |

---

## Quick Start

**Requirements:** Linux kernel 5.x+ · Go 1.25+ · root privileges

```bash
git clone https://github.com/i-OmSharma/veil.git
cd veil
go build -o veil ./cmd/veil

# Run an alpine shell
sudo ./veil run alpine:latest /bin/sh

# Node server with port forwarding
sudo ./veil run -p 3000:3000 node:20-alpine /bin/sh -c "node server.js"
```

Or use the pre-built binary:

```bash
chmod +x veil-linux-amd64
sudo ./veil-linux-amd64 run alpine:latest /bin/sh
```

---

## Commands

### `veil run` — Run a container

```
sudo veil run [flags] <image> <command...>
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--memory` | `-m` | `0` (unlimited) | Memory limit in bytes |
| `--cpu-quota` | | `0` (unlimited) | CPU time per period (microseconds) |
| `--cpu-period` | | `100000` (100ms) | CPU scheduling period (microseconds) |
| `--no-net` | | `false` | Share host network stack instead of isolating |
| `--env` | `-e` | | Set env variable `KEY=VALUE` (repeatable) |
| `--publish` | `-p` | | Port mapping `host:container` (repeatable) |
| `--volume` | `-v` | | Bind mount `host:container` (repeatable) |

**Examples:**

```bash
# Interactive shell
sudo veil run ubuntu:22.04 /bin/bash

# Memory + CPU limits (128 MB, 50% of one core)
sudo veil run -m 134217728 --cpu-quota 50000 ubuntu:22.04 /bin/bash

# Environment variables
sudo veil run -e DEBUG=true -e PORT=8080 ubuntu:22.04 /bin/bash

# Volume mount
sudo veil run -v /home/user/app:/app ubuntu:22.04 /bin/bash

# Port forwarding
sudo veil run -p 8080:80 nginx:latest nginx -g 'daemon off;'

# No network isolation
sudo veil run --no-net ubuntu:22.04 /bin/bash

# Combined flags
sudo veil run -m 268435456 -p 3000:3000 -v /home/user/code:/code -e NODE_ENV=production node:20 node app.js
```

---

### `veil pull` — Pull an OCI image

Downloads and extracts an image from any OCI-compliant registry. Subsequent pulls of the same image are skipped (cached).

```bash
sudo veil pull ubuntu:22.04
sudo veil pull alpine:latest
sudo veil pull ghcr.io/owner/repo:tag
```

Images cached at: `/var/lib/veil/images/<registry>__<repo>__<tag>/rootfs`

---

### `veil push` — Push a directory as an OCI image

Packs a local directory into an OCI image and uploads it to a registry.

```bash
sudo veil push /path/to/rootfs registry.example.com/myimage:v1
```

---

### `veil ps` — List containers

```bash
sudo veil ps
```

```
ID           IMAGE                STATUS     COMMAND
a1b2c3d4     ubuntu:22.04         running    [/bin/bash]
e5f6g7h8     alpine:latest        exited     [/bin/sh]
```

---

### `veil stop` — Stop a container

Sends SIGTERM, polls every 500 ms for up to 5 s, then SIGKILLs. Supports short ID prefixes.

```bash
sudo veil stop a1b2c3d4
sudo veil stop a1b2          # prefix match
```

---

### `veil images` — List cached images

```bash
sudo veil images
```

```
Local images:
 - index.docker.io__library__ubuntu__22.04
 - index.docker.io__library__alpine__latest
```

---

## How It Works

```
veil run ubuntu:22.04 /bin/bash
        │
        ├─ Pull image → /var/lib/veil/images/ubuntu.../rootfs
        ├─ Mount OverlayFS → /tmp/veil-<id>/merged  (writable layer)
        ├─ fork() + clone(CLONE_NEWPID | CLONE_NEWUTS | CLONE_NEWNS | CLONE_NEWIPC | CLONE_NEWNET)
        │         re-exec /proc/self/exe child ...
        │
        ├─ SIGSTOP child
        │   ├─ Apply cgroups  (/sys/fs/cgroup/veil-<pid>/)
        │   └─ Setup veth     (veth-<pid> ↔ eth0 inside container)
        ├─ SIGCONT child
        │
        └─ [child — new namespaces, becomes PID 1]
            ├─ MS_PRIVATE mount propagation
            ├─ sethostname(veil-<pid>)
            ├─ bind-mount /dev
            ├─ bind-mount user volumes
            ├─ pivot_root → container rootfs
            ├─ write /etc/resolv.conf  (8.8.8.8, 1.1.1.1)
            ├─ mount /proc
            └─ exec /bin/bash
```

**Network topology:**

```
Host side                              Container side
────────────────────────────────────────────────────
[veil0 bridge: 10.88.0.1/16]
      │
[veth-<pid>]  ←── veth pair ──→  [eth0: 10.88.0.2/16]
      │
iptables MASQUERADE → host NIC → internet
```

---

## Project Structure

```
veil/
├── cmd/
│   └── veil/
│       └── main.go               ← CLI entry point (cobra commands)
├── internal/
│   ├── cgroup/
│   │   └── cgroup.go             ← cgroups v2 resource limits
│   ├── container/
│   │   ├── run.go                ← orchestration layer
│   │   └── child.go              ← container init process (PID 1)
│   ├── image/
│   │   ├── image.go              ← pull / push / extract OCI images
│   │   └── types.go              ← ImageRef type
│   ├── network/
│   │   └── network.go            ← bridge, veth pair, iptables
│   ├── overlayfs/
│   │   └── overlayfs.go          ← overlay mount + pivot_root
│   └── state/
│       └── state.go              ← container state (disk persistence)
├── go.mod
├── veil-linux-amd64              ← pre-built binary
└── README.md
```

---

## Storage Paths

| Path | Purpose |
|------|---------|
| `/var/lib/veil/images/` | Cached image rootfs directories |
| `/var/lib/veil/state.json` | Container state (ID, PID, status, etc.) |
| `/tmp/veil-<id>/` | Per-container OverlayFS (upper, work, merged) |
| `/sys/fs/cgroup/veil-<pid>/` | Per-container cgroup (deleted on exit) |

---

## Internals Reference

<details>
<summary><strong>cmd/veil/main.go</strong></summary>

CLI entry point. Registers all cobra subcommands and flags. Also intercepts the `child` re-exec path — when veil spawns a container it re-execs itself with `child` as the first argument; `main.go` detects this and hands off to `container.Child()` before cobra processes args.

</details>

<details>
<summary><strong>internal/container/run.go</strong></summary>

Orchestration layer — the heart of veil. `Run()` wires every subsystem together:

1. Resolve image → pull if not cached
2. Mount OverlayFS (copy-on-write layer)
3. Register container in state
4. `clone()` new namespaces (PID, UTS, Mount, IPC, Net)
5. `SIGSTOP` child — freeze before it runs any code
6. Apply cgroups (memory + CPU limits) while frozen
7. Create veth pair and wire into bridge while frozen
8. `SIGCONT` — resume with limits and network in place
9. `Wait()` until exit
10. Cleanup: cgroup → port forward rules → veth → overlay unmount

</details>

<details>
<summary><strong>internal/container/child.go</strong></summary>

Runs inside the new namespaces — becomes PID 1 of the container:

- Make mount namespace private (`MS_PRIVATE|MS_REC`)
- Set hostname (`veil-<pid>`)
- Bind-mount `/dev` from host
- Bind-mount user volumes (`-v` flag) before `pivot_root`
- `pivot_root` → swap container rootfs for host `/`
- Write `/etc/resolv.conf` (Google + Cloudflare DNS)
- Mount fresh `/proc`
- `exec()` user's command — replaces itself with the container process

</details>

<details>
<summary><strong>internal/cgroup/cgroup.go</strong></summary>

Creates `/sys/fs/cgroup/veil-<pid>/`, enables `memory` and `cpu` controllers, writes limits, moves container PID into cgroup. `Remove()` deletes the cgroup directory on exit.

```
/sys/fs/cgroup/
└── veil-<pid>/
    ├── memory.max       ← hard RAM limit
    ├── memory.swap.max  ← 0 (swap disabled)
    ├── cpu.max          ← "quota period" throttle
    ├── memory.oom_group ← kill whole cgroup on OOM
    └── cgroup.procs     ← container PID
```

</details>

<details>
<summary><strong>internal/network/network.go</strong></summary>

- **`SetupBridge()`** — creates `veil0`, assigns `10.88.0.1/16`, enables IP forwarding, adds MASQUERADE rule. Idempotent.
- **`SetupVeth()`** — creates veth pair, attaches host end to bridge, moves container end into container netns, configures IP + default route via `nsenter`.
- **`SetupPortForward()`** — adds iptables DNAT + FORWARD rules to map host port → container port.
- **`CleanupVeth()`** / **`CleanupPortForward()`** — teardown on container exit.

</details>

<details>
<summary><strong>internal/overlayfs/overlayfs.go</strong></summary>

Mounts OverlayFS so every container gets its own writable layer on top of the read-only image rootfs. Container writes go to `/tmp/veil-<id>/upper/`; the image is never modified. Also implements `PivotRoot()` using `syscall.PivotRoot`.

</details>

<details>
<summary><strong>internal/state/state.go</strong></summary>

Persists container metadata to `/var/lib/veil/state.json`. Tracks: container ID, PID, image, command, status (`running`/`exited`), created time, rootfs path. `Stop()` sends SIGTERM, polls every 500 ms for up to 5 s, then SIGKILLs.

</details>

---

## Limitations (v0.1)

- **Single container networking** — static IP `10.88.0.2/16`; only one container can use the network at a time (no IPAM pool yet)
- **Root only** — all commands require `sudo`
- **Linux only** — uses Linux-specific syscalls throughout
- **No resource limits by default** — `--memory` and `--cpu-quota` must be set explicitly

---

## Contributing

Issues and PRs welcome. Keep changes focused — one concern per PR.

```bash
git clone https://github.com/i-OmSharma/veil.git
cd veil
go build -o veil ./cmd/veil
sudo ./veil run alpine:latest /bin/sh   # smoke test
```

---

## License

MIT — see [LICENSE](LICENSE).

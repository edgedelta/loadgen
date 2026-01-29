# Benchmark Scripts

Scripts for running standardized load benchmarks against log collector/ingestion services (EdgeDelta, ObservIQ, Cribl) using [httploggen](../README.md).

## Overview

| Script | Purpose |
|--------|---------|
| **trigger_benchmark.sh** | Entry point: selects app, creates logs, and invokes the benchmark runner |
| **run_benchmark.sh** | Runs the actual benchmark: starts the service, drives httploggen at multiple worker levels, collects stats |

## Quick Start

```bash
# From the project root
cd scripts

# Interactive: choose app from menu (edgedelta, observiq, cribl)
./trigger_benchmark.sh

# Or pass the app directly
./trigger_benchmark.sh edgedelta
./trigger_benchmark.sh observiq
./trigger_benchmark.sh cribl
```

Output is written to `benchmark_results/<app>_<timestamp>.log` and also printed to the terminal.

---

## Requirements

- **Linux** with systemd (services are started/stopped via `systemctl`)
- **sudo** for `systemctl start` / `systemctl stop`
- **httploggen** built at the project root: `go build -o httploggen` in the repo root
- **Run from `scripts/`**: both scripts expect to be executed from the `scripts/` directory

### Supported Apps and Services

| App | systemd service | Port |
|-----|-----------------|------|
| edgedelta | `edgedelta.service` | 8085 |
| observiq | `observiq-otel-collector` | 7075 |
| cribl | `cribl-edge.service` | 6085 |

The corresponding service must be installed and configured to listen on the port above before running the benchmark.

---

## trigger_benchmark.sh

Orchestrates a full benchmark run: app selection, log capture, and invocation of `run_benchmark.sh`.

### Usage

```bash
./trigger_benchmark.sh [app]
```

- **`app`** (optional): `edgedelta`, `observiq`, or `cribl`. If omitted, a menu is shown to select one.

### Behavior

1. Resolves `app` (from argument or interactive `select`).
2. Creates `benchmark_results/` if it does not exist.
3. Ensures `./run_benchmark.sh` exists and is executable.
4. Runs `./run_benchmark.sh "$app"` and tees output to:
   - `benchmark_results/<app>_<YYYYMMDD_HHMMSS>.log`
   - stdout

### Example

```bash
./trigger_benchmark.sh observiq
# Log file: benchmark_results/observiq_20260128_143022.log
```

---

## run_benchmark.sh

Runs the httploggen benchmark for a given app: manages the systemd service, then drives httploggen at 80, 100, and 120 workers with process monitoring.

### Usage

```bash
./run_benchmark.sh <app>
```

- **`app`** (required): `edgedelta`, `observiq`, or `cribl`.

### Behavior

1. **Service startup**
   - Starts the systemd service for the chosen app.
   - Waits 30 seconds, then checks that the service is active. Exits on failure.

2. **Cleanup**
   - Uses a `trap` on `EXIT` and `ERR` to run `systemctl stop <service>` so the service is stopped even if the script or httploggen fails.

3. **Benchmark iterations**
   - For each of **80, 100, 120** workers:
     - Runs httploggen with:
       - `--endpoint http://localhost:<port>`
       - `--format nginx_log`
       - `--number 1`
       - `--workers <80|100|120>`
       - `--period 1ms`
       - `--total-time 1m`
       - Process monitoring:
         - **edgedelta, observiq**: `--monitor-process <app>`
         - **cribl**: `--monitor-pid <cribl server pid>`, plus a separate monitor for the `cribl.js` process.
     - Waits 60 seconds before the next worker level.

### httploggen parameters (summary)

| Option | Value | Purpose |
|--------|-------|---------|
| `--endpoint` | `http://localhost:<port>` | Target for the app (8085, 7075, or 6085) |
| `--format` | `nginx_log` | Log format |
| `--number` | 1 | Logs per worker per period |
| `--workers` | 80, 100, 120 | Concurrency (one level per iteration) |
| `--period` | 1ms | Interval between sends (high load) |
| `--total-time` | 1m | Duration per iteration |
| `--monitor-process` / `--monitor-pid` | app-specific | CPU, memory, threads of the collector |

---

## Output and Logs

### Console

You’ll see httploggen’s `[STATS]` and `[MONITOR]` lines, plus any errors, for each worker run.

### Log files

- Directory: `scripts/benchmark_results/`
- Name: `<app>_<YYYYMMDD_HHMMSS>.log`
- Content: full stdout/stderr of `run_benchmark.sh` for that run.

Example:

```text
benchmark_results/
  edgedelta_20260128_143022.log
  observiq_20260128_150311.log
  cribl_20260128_161045.log
```

---

## Running from the Project Root

If you prefer to run from the repo root:

```bash
# From project root
./scripts/trigger_benchmark.sh edgedelta
```

This will fail unless `run_benchmark.sh` and `../httploggen` work from `scripts/`. `run_benchmark.sh` uses `../httploggen`, so the repo must be laid out as:

```text
<repo>/
  httploggen      # binary
  scripts/
    run_benchmark.sh
    trigger_benchmark.sh
```

`trigger_benchmark.sh` also expects `./run_benchmark.sh` to exist in the current directory, so it should be run with `scripts/` as the current working directory:

```bash
cd scripts && ./trigger_benchmark.sh edgedelta
```

---

## Typical Workflow

1. Build httploggen: `go build -o httploggen` (in project root).
2. Ensure the target service is installed and the correct systemd unit and port are in use.
3. `cd scripts`
4. `./trigger_benchmark.sh [edgedelta|observiq|cribl]`
5. Inspect `benchmark_results/<app>_<timestamp>.log` for throughput, errors, and monitor stats.

---

## Troubleshooting

| Issue | What to check |
|-------|----------------|
| `run_benchmark.sh not found or not executable` | Run from `scripts/` and `chmod +x run_benchmark.sh` if needed. |
| `Missing app` / `Invalid app` | Pass `edgedelta`, `observiq`, or `cribl` to `run_benchmark.sh`. |
| `Failed to start <service>` | Service installed? `systemctl status <service>`. |
| `Service not active after start` | Service logs: `journalctl -u <service> -n 50`. |
| `Connection refused` or similar from httploggen | Service listening on the expected port? (`ss -tlnp` or `netstat -tlnp`). |
| `../httploggen` not found | Build httploggen in the project root and run from `scripts/`. |

---

## Cribl-specific behavior

For **cribl**, `run_benchmark.sh`:

- Finds PIDs for `cribl.js` and `cribl server`.
- Starts an extra httploggen monitor for `cribl.js` in the background.
- Runs the main httploggen with `--monitor-pid` for the `cribl server` process.
- Stops the `cribl.js` monitor at the end of each iteration.

If the Cribl processes or names differ on your system, you may need to adjust the `grep` patterns in `run_benchmark.sh`.

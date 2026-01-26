# Example Test Scripts

Pre-configured test scripts for common use cases.

## Setup

1. Build httploggen in the parent directory:
   ```bash
   cd ..
   go build -o httploggen main.go
   ```

2. Make scripts executable:
   ```bash
   chmod +x *.sh
   ```

## Usage

### Basic Load Test

Test at 1000 logs/sec for 30 seconds:

```bash
./basic-test.sh
```

With custom endpoint:
```bash
ENDPOINT=http://my-collector:8080 ./basic-test.sh
```

### High Throughput Test

Run the same test used in our blog post (~48K logs/sec):

```bash
./high-throughput-test.sh
```

### Continuous Mode (Maximum Speed)

Send as fast as possible:

```bash
./continuous-mode.sh
```

With custom workers and duration:
```bash
WORKERS=50 DURATION=2m ./continuous-mode.sh
```

### PII Masking Test

Test your PII masking/redaction pipeline:

```bash
./pii-test.sh
```

### Process Monitoring (Standalone)

Monitor a process without generating load:

```bash
# Monitor by process name
./monitor-only.sh edgedelta

# Monitor by PID
./monitor-only.sh --monitor-pid 12345
```

Output:
```
[MONITOR] pid: 12345 | cpu: 45.3% | memory: 234.5MB | threads: 28
```

### Process Monitoring with Load

Monitor a process while generating load to observe performance impact:

```bash
# Monitor edgedelta process
./monitor-with-load.sh edgedelta

# Monitor different process
./monitor-with-load.sh myapp
```

Output shows both load generation stats and process metrics:
```
[STATS] current: 1000 logs/sec, 0.64 MB/s | avg: 1000 logs/sec | total: 5000 | errors: 0 | backpressure: 0 (0.0%)
[MONITOR] pid: 12345 | cpu: 45.3% | memory: 234.5MB | threads: 28
```

## Environment Variables

All scripts support the following environment variables:

- `ENDPOINT`: HTTP endpoint (default: http://localhost:8085)
- `WORKERS`: Number of workers (script-specific defaults)
- `DURATION`: Test duration (script-specific defaults)

## Example Outputs

### Basic Test
```
[STATS] current: 998.23 logs/sec, 0.64 MB/s | avg: 1000.12 logs/sec |
        total: 30004 | errors: 0 | backpressure: 0 (0.0%)
```

### High Throughput Test
```
[STATS] current: 47817.14 logs/sec, 27.41 MB/s | avg: 47778.82 logs/sec |
        total: 477792 | errors: 0 | backpressure: 0 (0.0%)
```

## Customization

Feel free to modify these scripts for your specific testing needs. The scripts are simple shell wrappers around the httploggen binary.

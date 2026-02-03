# HTTP Log Generator

A high-performance HTTP log generator for load testing log ingestion pipelines and observability systems.

## Features

- **High throughput**: 40K+ logs/sec with payload pool optimization
- **Randomized data**: Payload pool with configurable variety (default 100 unique payloads)
- **Multiple formats**: nginx, apache, masked PII data, Datadog
- **Industry standard output**: NDJSON (newline-delimited JSON) by default, with JSON array and single object modes
- **Realistic data**: Generates fake PII (emails, credit cards, IBANs, MAC addresses)
- **Continuous mode**: Maximum speed testing with `--period 0`
- **Real-time stats**: Current and average throughput metrics
- **Backpressure detection**: Tracks 429/503 responses
- **Process monitoring**: Monitor CPU, memory, and threads of any process
- **Cross-platform**: Supports macOS and Linux
- **Zero dependencies**: Only requires `gofakeit` for data generation

## Installation

```bash
# Clone the repository
git clone https://github.com/edgedelta/loadgen.git
cd loadgen

# Build (builds all .go files in the package)
go build -o loadgen

# Or run directly
go run . --help
```

## Quick Start

### Basic Load Test

```bash
# Send 1000 logs/sec to your endpoint
./loadgen \
    --endpoint http://localhost:8085 \
    --format nginx_log \
    --number 1 \
    --workers 10 \
    --period 100ms
```

### High Throughput Test

Test at ~48K logs/sec (as used in our blog post):

```bash
./loadgen \
    --endpoint http://localhost:8085 \
    --format nginx_log \
    --number 1 \
    --workers 120 \
    --period 1ms \
    --total-time 1m
```

**Expected output:**
```
[STATS] current: 47817.14 logs/sec, 27.41 MB/s | avg: 47778.82 logs/sec |
        total: 477792 | errors: 0 | backpressure: 0 (0.0%)
```

### Maximum Throughput (Continuous Mode)

```bash
# Send as fast as possible
./loadgen \
    --endpoint http://localhost:8085 \
    --format nginx_log \
    --number 100 \
    --workers 100 \
    --period 0 \
    --total-time 1m
```

## Command Line Options

```
Usage: loadgen [options]

Load Generation Options:
  -endpoint string
        HTTP endpoint to send logs to (default "http://localhost:4547")

  -format string
        Log format: nginx_log, apache_combined, masked_log, datadog (default "nginx_log")

  -format-style string
        Format style: ndjson (newline-delimited JSON), array (JSON array), single (single JSON object)
        (default "ndjson")

  -number int
        Number of logs per worker per period (default 1)

  -workers int
        Number of concurrent workers (default 1)

  -period duration
        Period between requests; use 0 for continuous mode (default 2s)

  -total-time duration
        Total test duration; 0 means forever (default 0s)

  -content-type string
        Content-Type header (default "application/json")

  -timeout duration
        HTTP request timeout (default 30s)

  -payload-pool-size int
        Number of unique payloads to generate in the pool (default 100)

Process Monitoring Options:
  -monitor-process string
        Process name to monitor (e.g., 'edgedelta')

  -monitor-pid int
        PID of process to monitor (0 means disabled)

  -monitor-interval duration
        Interval for process monitoring stats (default 5s)
```

## Log Formats

All formats support three output styles via `--format-style`:
- **ndjson** (default): Newline-delimited JSON - industry standard for log ingestion
- **array**: JSON array format `[{...}, {...}]`
- **single**: Single JSON object per request

### nginx_log (Default)

Generates nginx access logs with PII data in NDJSON format (default):

```json
{"@timestamp":"2026-01-23T15:30:45Z","message":"192.168.1.1 - - [23/Jan/2026:15:30:45 +0000] \"GET /api/users HTTP/1.1\" 200 1234","app_type":"nginx","pii":{"email":"john.doe@example.com","ipv4":"192.168.1.1","ipv6":"2001:0db8:85a3::8a2e:0370:7334","visa":"4111111111111111","mastercard":"5500000000000004","iban":"GB82WEST12345698765432","mac":"00:0a:95:9d:68:16","product":"Awesome Widget","color":"Blue","status":"active"}}
{"@timestamp":"2026-01-23T15:30:46Z","message":"203.0.113.42 - - [23/Jan/2026:15:30:46 +0000] \"POST /api/orders HTTP/1.1\" 201 5678","app_type":"nginx","pii":{"email":"jane.smith@example.com","ipv4":"203.0.113.42","ipv6":"2001:0db8:85a3::8a2e:0370:7335","visa":"4222222222222222","mastercard":"5400000000000005","iban":"FR7630006000011234567890189","mac":"00:0a:95:9d:68:17","product":"Premium Service","color":"Red","status":"pending"}}
```

**Note**: Each log entry is a complete JSON object on its own line. This is the standard format used by Elasticsearch, Datadog, EdgeDelta, and most log ingestion systems.

To use JSON array format instead:
```bash
./loadgen --format nginx_log --format-style array
```

### apache_combined

Apache combined log format with PII data (NDJSON by default):

```json
{"@timestamp":"2026-01-23T15:30:45Z","message":"192.168.1.1 - - [23/Jan/2026:15:30:45 +0000] \"GET /index.html HTTP/1.1\" 200 5432 \"-\" \"Mozilla/5.0\"","app_type":"apache","pii":{"email":"user@test.com","ipv4":"10.0.0.1","ipv6":"2001:0db8:85a3::1","visa":"4111111111111111","mastercard":"5500000000000004","iban":"DE89370400440532013000","mac":"1a:2b:3c:4d:5e:6f","product":"Standard Widget","color":"Green","status":"completed"}}
```

### masked_log

Pure PII data for testing masking/redaction (NDJSON by default):

```json
{"email":"user@example.com","ipv4":"203.0.113.42","ipv6":"2001:0db8:85a3::8a2e:0370:7334","visa":"4111111111111111","mastercard":"5500000000000004","iban":"DE89370400440532013000","mac":"00:0a:95:9d:68:16","product":"Premium Service","color":"Red","status":"pending"}
{"email":"admin@test.org","ipv4":"198.51.100.23","ipv6":"2001:0db8:85a3::8a2e:0370:7335","visa":"4222222222222222","mastercard":"5400000000000005","iban":"GB82WEST12345698765432","mac":"aa:bb:cc:dd:ee:ff","product":"Enterprise Plan","color":"Blue","status":"active"}
```

### datadog

Generates logs in the Datadog agent log format. The Datadog API expects JSON arrays, so use `--format-style array`:

```bash
./loadgen \
    --endpoint http://localhost:8126/api/v2/logs \
    --format datadog \
    --format-style array \
    --number 200 \
    --workers 8 \
    --period 100ms
```

Output (JSON array):
```json
[
  {
    "message": "synthetic datadog log id=0 level=info 192.168.1.1 - - [29/Jan/2026:15:30:45 +0000] \"GET /api/users HTTP/1.1\" 200 1234",
    "status": "info",
    "timestamp": 1738164645000,
    "hostname": "host-0",
    "service": "web-api",
    "ddsource": "go",
    "ddtags": "env:local,team:edgedelta,source:loadgen"
  }
]
```

## Usage Examples

### Test Local Log Collector

```bash
# Start your log collector (e.g., Fluentd, Logstash, etc.)
# Then send 10 logs/sec for 30 seconds

./loadgen \
    --endpoint http://localhost:8080/logs \
    --format nginx_log \
    --number 10 \
    --period 1s \
    --total-time 30s
```

### Stress Test with Multiple Workers

```bash
# 50 workers × 10 logs/sec = 500 logs/sec total
./loadgen \
    --endpoint http://localhost:8080/logs \
    --format nginx_log \
    --number 1 \
    --workers 50 \
    --period 20ms \
    --total-time 2m
```

### Test PII Masking

```bash
# Send pure PII data to test your masking pipeline
./loadgen \
    --endpoint http://localhost:8080/logs \
    --format masked_log \
    --number 100 \
    --period 1s
```

### Plain Text Logs

```bash
# Send plain text instead of JSON
./loadgen \
    --endpoint http://localhost:8080/logs \
    --format nginx_log \
    --content-type text/plain \
    --number 10 \
    --period 1s
```

### Process Monitoring

Monitor a process without generating load:

```bash
# Monitor by process name
./loadgen --monitor-process edgedelta

# Monitor by PID
./loadgen --monitor-pid 12345

# Custom monitoring interval (default is 5s)
./loadgen --monitor-process myapp --monitor-interval 2s
```

**Output:**
```
[MONITOR] pid: 12345 | cpu: 45.3% | memory: 234.5MB | threads: 28
```

Monitor a process while generating load:

```bash
# Observe how load impacts the monitored process
./loadgen \
    --endpoint http://localhost:8085 \
    --format nginx_log \
    --workers 48 \
    --period 10ms \
    --monitor-process edgedelta
```

**Output:**
```
[STATS] current: 1000 logs/sec, 0.64 MB/s | avg: 1000 logs/sec | total: 5000 | errors: 0 | backpressure: 0 (0.0%)
[MONITOR] pid: 12345 | cpu: 45.3% | memory: 234.5MB | threads: 28
```

**Monitoring Metrics:**
- **cpu**: CPU usage percentage
- **memory**: Resident memory (RSS) in megabytes
- **threads**: Number of threads

**Platforms Supported:** macOS and Linux

## Understanding the Stats

The tool prints real-time statistics every 5 seconds:

```
[STATS] current: 47817.14 logs/sec, 27.41 MB/s | avg: 47778.82 logs/sec |
        total: 477792 | errors: 0 | backpressure: 0 (0.0%)
```

- **current**: Throughput over the last 5 seconds (instantaneous rate)
- **avg**: Average throughput since start (overall performance)
- **total**: Total number of logs sent
- **errors**: HTTP errors (4xx/5xx responses)
- **backpressure**: 429/503 responses indicating server overload

## Performance Tips

1. **High stable throughput**: Use small periods (1-10ms) with many workers (48-120)
2. **Maximum speed**: Use `--period 0` for continuous mode
3. **Predictable load**: Use larger periods (1s-10s) for steady QPS
4. **CPU scaling**: Typically 8-16 workers per CPU core works well
5. **Payload variety**: Adjust `--payload-pool-size` (default 100) to balance variety vs memory
   - Smaller pools (10-50): Less memory, faster startup, sufficient for most load tests
   - Larger pools (500-1000): More variety, useful for testing unique value handling
6. **Format style**: NDJSON (`--format-style ndjson`) is default and works with most log systems

## How It Works

1. **Payload pool**: Pre-generates a pool of unique payloads at startup (default 100 variants)
2. **Random selection**: Each request randomly selects payloads from the pool for data variety
3. **Concurrent workers**: Each worker operates independently sending HTTP POST requests
4. **Periodic or continuous**: Workers either wait for ticks (`--period`) or loop continuously (`--period 0`)
5. **Realistic data**: Uses [gofakeit](https://github.com/brianvoe/gofakeit) to generate fake PII data
6. **HTTP optimization**: Connection pooling, keep-alive, and disabled compression for maximum throughput
7. **NDJSON output**: Newline-delimited JSON format by default for maximum compatibility with log systems

## Benchmarking Results

From our [blog post](https://edgedelta.com/blog) comparing log collectors:

### Test Command
```bash
./loadgen --endpoint http://localhost:8085 \
    --format nginx_log --number 1 --workers 120 \
    --period 1ms --total-time 1m
```

### Build and Run

```bash
docker build -t loadgen .

docker run loadgen \
    --endpoint http://host.docker.internal:8085 \
    --format nginx_log \
    --number 1 \
    --workers 48 \
    --period 1ms \
    --total-time 1m
```

## Troubleshooting

### Connection Refused

```
Failed to send HTTP request: dial tcp: connect: connection refused
```

**Fix**: Ensure your log collector is running and listening on the specified endpoint.

### Low Throughput

If you're not reaching the expected throughput:

1. **Check CPU**: Run `top` - if loadgen isn't using multiple cores, increase `--workers`
2. **Network limits**: Test with `localhost` first to rule out network issues
3. **Server bottleneck**: Check if the receiving server is the bottleneck (high CPU, backpressure)
4. **Period too large**: Try smaller `--period` or use `--period 0` for maximum speed

### Memory Growth

Memory usage is expected to grow slightly during startup as payloads are cached. This is normal and stabilizes after the first few seconds.

## Development

### Run Tests

```bash
go test -v ./...
```

### Build for Multiple Platforms

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o loadgen-linux

# macOS
GOOS=darwin GOARCH=amd64 go build -o loadgen-macos

# Windows
GOOS=windows GOARCH=amd64 go build -o loadgen.exe
```

## Contributing

Contributions welcome! Please submit issues and pull requests to the repository.

## License

Apache License 2.0

## Support

- **GitHub Issues**: Report bugs or request features
- **Website**: https://edgedelta.com
- **Blog**: Read our performance comparison blog post

## Credits

Built by the EdgeDelta team for benchmarking log ingestion pipelines.

Uses [gofakeit](https://github.com/brianvoe/gofakeit) for generating realistic fake data.

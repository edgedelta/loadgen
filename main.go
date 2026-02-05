package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/brianvoe/gofakeit/v7"
)

// Config holds the load generator configuration.
type Config struct {
	Endpoint        string
	Period          time.Duration
	Format          string
	FormatStyle     string
	Number          int
	ContentType     string
	Timeout         time.Duration
	TotalTime       time.Duration
	Workers         int
	MonitorPID      int
	MonitorProcess  string
	MonitorSelf     bool
	MonitorInterval time.Duration
	PayloadPoolSize int
}

// LogMessage represents a structured log entry.
type LogMessage struct {
	Timestamp string            `json:"@timestamp"`
	Message   string            `json:"message"`
	AppType   string            `json:"app_type,omitempty"`
	PII       map[string]string `json:"pii,omitempty"`
}

// datadogLog represents a Datadog agent log entry.
type datadogLog struct {
	Message   string `json:"message"`
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
	Hostname  string `json:"hostname"`
	Service   string `json:"service"`
	Source    string `json:"ddsource"`
	Tags      string `json:"ddtags"`
}

// MaskedData contains fake PII data for testing.
type MaskedData struct {
	Email      string `json:"email"`
	IPv4       string `json:"ipv4"`
	IPv6       string `json:"ipv6"`
	Visa       string `json:"visa"`
	Mastercard string `json:"mastercard"`
	IBAN       string `json:"iban"`
	MAC        string `json:"mac"`
	Product    string `json:"product"`
	Color      string `json:"color"`
	Status     string `json:"status"`
}

// PayloadPool holds pre-generated and pre-marshaled payloads for variety without performance impact.
type PayloadPool struct {
	// Pre-marshaled JSON bytes for each format (one JSON object per slice entry).
	nginxLogsBytes  [][]byte
	apacheLogsBytes [][]byte
	maskedDataBytes [][]byte
	datadogBytes    [][]byte
	// Raw text logs (not JSON).
	nginxRawLogs  []string
	apacheRawLogs []string
}

// Stats tracks performance metrics.
type Stats struct {
	mu                sync.Mutex
	successfulLogs    int64
	totalLogs         int64
	totalBytes        int64
	httpErrors        int64
	backpressureCount int64
	startTime         time.Time
	lastPrintTime     time.Time
	lastPrintLogs     int64
	lastPrintBytes    int64
}

func (s *Stats) recordSuccess(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.successfulLogs++
	s.totalLogs++
	s.totalBytes += int64(bytes)
}

func (s *Stats) recordError(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalLogs++
	s.totalBytes += int64(bytes)
	s.httpErrors++
}

func (s *Stats) recordBackpressure(bytes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalLogs++
	s.totalBytes += int64(bytes)
	s.httpErrors++
	s.backpressureCount++
}

func (s *Stats) print() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(s.startTime).Seconds()
	if elapsed == 0 {
		elapsed = 1
	}

	avgLogsPerSec := float64(s.successfulLogs) / elapsed
	avgThroughputMBps := float64(s.totalBytes) / elapsed / 1024 / 1024

	var currentLogsPerSec float64
	var currentThroughputMBps float64
	if !s.lastPrintTime.IsZero() {
		intervalSec := now.Sub(s.lastPrintTime).Seconds()
		if intervalSec > 0 {
			logsSinceLastPrint := s.successfulLogs - s.lastPrintLogs
			bytesSinceLastPrint := s.totalBytes - s.lastPrintBytes
			currentLogsPerSec = float64(logsSinceLastPrint) / intervalSec
			currentThroughputMBps = float64(bytesSinceLastPrint) / intervalSec / 1024 / 1024
		}
	}

	s.lastPrintTime = now
	s.lastPrintLogs = s.successfulLogs
	s.lastPrintBytes = s.totalBytes

	backpressurePct := float64(0)
	if s.totalLogs > 0 {
		backpressurePct = float64(s.backpressureCount) * 100 / float64(s.totalLogs)
	}

	if currentLogsPerSec > 0 {
		fmt.Printf("[STATS] current: %.2f logs/sec, %.2f MB/s | avg: %.2f logs/sec | total: %d | errors: %d | backpressure: %d (%.1f%%)\n",
			currentLogsPerSec, currentThroughputMBps, avgLogsPerSec, s.totalLogs, s.httpErrors, s.backpressureCount, backpressurePct)
	} else {
		fmt.Printf("[STATS] avg: %.2f logs/sec | total: %d | throughput: %.2f MB/s | errors: %d | backpressure: %d (%.1f%%)\n",
			avgLogsPerSec, s.totalLogs, avgThroughputMBps, s.httpErrors, s.backpressureCount, backpressurePct)
	}
}

func main() {
	config := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Println("Received shutdown signal")
		cancel()
	}()

	// Set up total time limit if specified.
	if config.TotalTime > 0 {
		go func() {
			time.Sleep(config.TotalTime)
			log.Println("Total time limit reached, shutting down")
			cancel()
		}()
	}

	// Determine monitoring targets.
	var targetStats *ProcessStats
	var selfStats *ProcessStats

	if config.MonitorProcess != "" {
		pid, err := findProcessByName(config.MonitorProcess)
		if err != nil {
			log.Fatalf("Failed to find process '%s': %v", config.MonitorProcess, err)
		}
		targetStats = &ProcessStats{
			pid:         pid,
			processName: config.MonitorProcess,
			monitorType: MonitorTypeTarget,
		}
		log.Printf("Found process '%s' with PID: %d", config.MonitorProcess, pid)
	} else if config.MonitorPID > 0 {
		targetStats = &ProcessStats{
			pid:         config.MonitorPID,
			monitorType: MonitorTypeTarget,
		}
		log.Printf("Monitoring process PID: %d", config.MonitorPID)
	}

	if config.MonitorSelf {
		selfStats = &ProcessStats{
			pid:         os.Getpid(),
			processName: "loadgen",
			monitorType: MonitorTypeSelf,
		}
		log.Printf("Monitoring self PID: %d", selfStats.pid)
	}

	// Determine mode: monitoring-only vs concurrent vs normal.
	monitoringEnabled := targetStats != nil || selfStats != nil
	endpointProvided := flag.Lookup("endpoint").Value.String() != flag.Lookup("endpoint").DefValue

	if monitoringEnabled && !endpointProvided {
		// Mode 2: Standalone monitoring only.
		log.Printf("Starting monitoring-only mode (interval: %s)", config.MonitorInterval)

		if targetStats != nil {
			go monitorProcess(ctx, targetStats, config.MonitorInterval)
		}
		if selfStats != nil {
			go monitorProcess(ctx, selfStats, config.MonitorInterval)
		}

		// Block until context is cancelled.
		<-ctx.Done()
		log.Println("Monitoring stopped")
		return
	}

	if monitoringEnabled {
		// Mode 1: Concurrent monitoring + load generation.
		log.Printf("Starting concurrent monitoring (interval: %s)", config.MonitorInterval)
		if targetStats != nil {
			go monitorProcess(ctx, targetStats, config.MonitorInterval)
		}
		if selfStats != nil {
			go monitorProcess(ctx, selfStats, config.MonitorInterval)
		}
	}

	// Normal load generation (with or without monitoring).
	log.Printf("Starting HTTP log generator - endpoint=%s format=%s workers=%d period=%s\n",
		config.Endpoint, config.Format, config.Workers, config.Period)

	if err := run(ctx, config); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func parseFlags() *Config {
	endpoint := flag.String("endpoint", "http://localhost:4547", "HTTP endpoint to send logs to")
	period := flag.Duration("period", 2*time.Second, "Period between log generations (use 0 for continuous mode)")
	format := flag.String("format", "nginx_log", "Log format: nginx_log, apache_combined, masked_log, datadog")
	formatStyle := flag.String("format-style", "ndjson", "Format style: ndjson (newline-delimited JSON), array (JSON array), single (single JSON object)")
	number := flag.Int("number", 1, "Number of logs per worker per period")
	contentType := flag.String("content-type", "application/json", "Content-Type header")
	timeout := flag.Duration("timeout", 30*time.Second, "HTTP request timeout")
	totalTime := flag.Duration("total-time", 0, "Total time to run (0 means forever)")
	workers := flag.Int("workers", 1, "Number of concurrent workers")
	monitorPID := flag.Int("monitor-pid", 0, "PID of process to monitor (0 means disabled)")
	monitorProcess := flag.String("monitor-process", "", "Process name to monitor (e.g., 'edgedelta')")
	monitorSelf := flag.Bool("monitor-self", false, "Monitor loadgen's own resource usage")
	monitorInterval := flag.Duration("monitor-interval", 5*time.Second, "Interval for process monitoring stats")
	payloadPoolSize := flag.Int("payload-pool-size", 100, "Number of unique payloads to generate in the pool")
	flag.Parse()

	return &Config{
		Endpoint:        *endpoint,
		Period:          *period,
		Format:          *format,
		FormatStyle:     *formatStyle,
		Number:          *number,
		ContentType:     *contentType,
		Timeout:         *timeout,
		TotalTime:       *totalTime,
		Workers:         *workers,
		MonitorPID:      *monitorPID,
		MonitorProcess:  *monitorProcess,
		MonitorSelf:     *monitorSelf,
		MonitorInterval: *monitorInterval,
		PayloadPoolSize: *payloadPoolSize,
	}
}

func generatePayloadPool(config *Config) *PayloadPool {
	pool := &PayloadPool{}
	poolSize := config.PayloadPoolSize

	log.Printf("Generating payload pool with %d variants...", poolSize)

	// Generate pool based on format and content-type.
	// Pre-marshal all JSON payloads to eliminate marshaling from hot path.
	switch config.Format {
	case "masked_log":
		pool.maskedDataBytes = make([][]byte, poolSize)
		for i := range poolSize {
			data := generateMaskedData()
			marshaled, err := json.Marshal(data)
			if err != nil {
				log.Fatalf("Failed to marshal masked data: %v", err)
			}
			pool.maskedDataBytes[i] = marshaled
		}

	case "datadog":
		pool.datadogBytes = make([][]byte, poolSize)
		baseTime := time.Now()
		for i := range poolSize {
			// Vary timestamps slightly for realism.
			t := baseTime.Add(time.Duration(i) * time.Millisecond)
			logEntry := generateDatadogLog(t, int64(i))
			marshaled, err := json.Marshal(logEntry)
			if err != nil {
				log.Fatalf("Failed to marshal datadog log: %v", err)
			}
			pool.datadogBytes[i] = marshaled
		}

	case "apache_combined":
		if config.ContentType == "application/json" {
			pool.apacheLogsBytes = make([][]byte, poolSize)
			baseTime := time.Now()
			for i := range poolSize {
				t := baseTime.Add(time.Duration(i) * time.Millisecond)
				logLine := generateApacheLog(t)
				maskedData := generateMaskedData()

				logMsg := LogMessage{
					Timestamp: t.Format(time.RFC3339),
					Message:   logLine,
					AppType:   "apache",
					PII:       convertMaskedDataToMap(maskedData),
				}
				marshaled, err := json.Marshal(logMsg)
				if err != nil {
					log.Fatalf("Failed to marshal apache log: %v", err)
				}
				pool.apacheLogsBytes[i] = marshaled
			}
		} else {
			pool.apacheRawLogs = make([]string, poolSize)
			baseTime := time.Now()
			for i := range poolSize {
				t := baseTime.Add(time.Duration(i) * time.Millisecond)
				pool.apacheRawLogs[i] = generateApacheLog(t)
			}
		}

	default: // nginx_log
		if config.ContentType == "application/json" {
			pool.nginxLogsBytes = make([][]byte, poolSize)
			baseTime := time.Now()
			for i := range poolSize {
				t := baseTime.Add(time.Duration(i) * time.Millisecond)
				logLine := generateNginxLog(t)
				maskedData := generateMaskedData()

				logMsg := LogMessage{
					Timestamp: t.Format(time.RFC3339),
					Message:   logLine,
					AppType:   "nginx",
					PII:       convertMaskedDataToMap(maskedData),
				}
				marshaled, err := json.Marshal(logMsg)
				if err != nil {
					log.Fatalf("Failed to marshal nginx log: %v", err)
				}
				pool.nginxLogsBytes[i] = marshaled
			}
		} else {
			pool.nginxRawLogs = make([]string, poolSize)
			baseTime := time.Now()
			for i := range poolSize {
				t := baseTime.Add(time.Duration(i) * time.Millisecond)
				pool.nginxRawLogs[i] = generateNginxLog(t)
			}
		}
	}

	log.Printf("Payload pool generated successfully")
	return pool
}

func run(ctx context.Context, config *Config) error {
	// Generate payload pool at startup.
	pool := generatePayloadPool(config)

	// Optimized HTTP client for high-throughput load testing.
	transport := &http.Transport{
		MaxIdleConns:        1000,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     0,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
		DisableKeepAlives:   false,
	}

	client := &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}

	stats := &Stats{
		startTime: time.Now(),
	}

	// Print stats every 5 seconds.
	statsTicker := time.NewTicker(5 * time.Second)
	defer statsTicker.Stop()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-statsTicker.C:
				stats.print()
			}
		}
	}()

	// Continuous mode: workers send as fast as possible.
	if config.Period == 0 {
		return runContinuous(ctx, client, config, stats, pool)
	}

	// Periodic mode: workers send at regular intervals.
	ticker := time.NewTicker(config.Period)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Context cancelled, stopping")
			stats.print()
			return nil
		case <-ticker.C:
			if err := sendLogs(ctx, client, config, stats, pool); err != nil {
				log.Printf("Failed to send logs: %v", err)
			}
		}
	}
}

func runContinuous(ctx context.Context, client *http.Client, config *Config, stats *Stats, pool *PayloadPool) error {
	var wg sync.WaitGroup

	// Spawn workers that continuously send requests.
	for i := 0; i < config.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					if err := sendBatch(client, config, config.Number, stats, pool); err != nil {
						log.Printf("Failed to send batch: %v", err)
					}
				}
			}
		}()
	}

	<-ctx.Done()
	log.Println("Context cancelled, stopping continuous mode")
	wg.Wait()
	stats.print()
	return nil
}

func sendLogs(ctx context.Context, client *http.Client, config *Config, stats *Stats, pool *PayloadPool) error {
	if config.Workers <= 1 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return sendBatch(client, config, config.Number, stats, pool)
		}
	}

	var wg sync.WaitGroup
	errChan := make(chan error, config.Workers)

	for i := 0; i < config.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				errChan <- ctx.Err()
			default:
				if err := sendBatch(client, config, config.Number, stats, pool); err != nil {
					errChan <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		if err != nil {
			return err
		}
	}
	return nil
}

// formatPayloadFromBytes formats a batch of pre-marshaled JSON payloads according to the specified format style.
func formatPayloadFromBytes(formatStyle string, numLogs int, poolBytes [][]byte) ([]byte, error) {
	if len(poolBytes) == 0 {
		return nil, fmt.Errorf("empty payload pool")
	}

	switch formatStyle {
	case "array":
		// JSON array format: [{...}, {...}]
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i := range numLogs {
			if i > 0 {
				buf.WriteByte(',')
			}
			randomPayload := poolBytes[rand.Intn(len(poolBytes))]
			buf.Write(randomPayload)
		}
		buf.WriteByte(']')
		return buf.Bytes(), nil

	case "single":
		// Single JSON object (only first item).
		randomPayload := poolBytes[rand.Intn(len(poolBytes))]
		return randomPayload, nil

	default: // ndjson
		// Newline-delimited JSON format.
		var buf bytes.Buffer
		for range numLogs {
			randomPayload := poolBytes[rand.Intn(len(poolBytes))]
			buf.Write(randomPayload)
			buf.WriteByte('\n')
		}
		return buf.Bytes(), nil
	}
}

// selectRandomStrings selects random strings from the pool and joins them with newlines.
func selectRandomStrings(pool []string, numLogs int) string {
	if len(pool) == 0 {
		return ""
	}

	selected := make([]string, numLogs)
	for i := range numLogs {
		selected[i] = pool[rand.Intn(len(pool))]
	}
	return strings.Join(selected, "\n") + "\n"
}

func sendBatch(client *http.Client, config *Config, numLogs int, stats *Stats, pool *PayloadPool) error {
	var body []byte
	var err error

	// Select random pre-marshaled payloads from the pool and format according to format-style.
	// No JSON marshaling happens here - just byte concatenation for maximum performance.
	switch config.Format {
	case "masked_log":
		body, err = formatPayloadFromBytes(config.FormatStyle, numLogs, pool.maskedDataBytes)
		if err != nil {
			return fmt.Errorf("failed to format masked data: %w", err)
		}

	case "datadog":
		body, err = formatPayloadFromBytes(config.FormatStyle, numLogs, pool.datadogBytes)
		if err != nil {
			return fmt.Errorf("failed to format datadog logs: %w", err)
		}

	case "apache_combined":
		if config.ContentType == "application/json" {
			body, err = formatPayloadFromBytes(config.FormatStyle, numLogs, pool.apacheLogsBytes)
			if err != nil {
				return fmt.Errorf("failed to format apache logs: %w", err)
			}
		} else {
			body = []byte(selectRandomStrings(pool.apacheRawLogs, numLogs))
		}

	default: // nginx_log
		if config.ContentType == "application/json" {
			body, err = formatPayloadFromBytes(config.FormatStyle, numLogs, pool.nginxLogsBytes)
			if err != nil {
				return fmt.Errorf("failed to format nginx logs: %w", err)
			}
		} else {
			body = []byte(selectRandomStrings(pool.nginxRawLogs, numLogs))
		}
	}

	req, err := http.NewRequest("POST", config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", config.ContentType)

	resp, err := client.Do(req)
	if err != nil {
		stats.recordError(len(body))
		return fmt.Errorf("failed to send HTTP request: %w", err)
	}

	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			stats.recordBackpressure(len(body))
			return fmt.Errorf("backpressure detected (status %d)", resp.StatusCode)
		}
		stats.recordError(len(body))
		return fmt.Errorf("HTTP request failed with status %d", resp.StatusCode)
	}

	stats.recordSuccess(len(body))
	return nil
}

func generateNginxLog(t time.Time) string {
	ips := []string{"192.168.1.1", "10.0.0.1", "172.16.0.1", "203.0.113.42", "198.51.100.23"}
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH"}
	paths := []string{"/api/users", "/api/products", "/api/orders", "/api/payments", "/health", "/metrics", "/login", "/logout"}
	statuses := []int{200, 201, 204, 301, 302, 400, 401, 403, 404, 500, 502, 503}

	return fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" %d %d",
		ips[rand.Intn(len(ips))],
		t.Format("02/Jan/2006:15:04:05 -0700"),
		methods[rand.Intn(len(methods))],
		paths[rand.Intn(len(paths))],
		statuses[rand.Intn(len(statuses))],
		rand.Intn(10000)+100,
	)
}

func generateApacheLog(t time.Time) string {
	ips := []string{"192.168.1.1", "10.0.0.1", "172.16.0.1", "203.0.113.42", "198.51.100.23"}
	methods := []string{"GET", "POST", "PUT", "DELETE"}
	paths := []string{"/index.html", "/about", "/contact", "/products", "/api/data"}
	statuses := []int{200, 201, 301, 302, 400, 404, 500}
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36",
		"curl/7.68.0",
	}

	return fmt.Sprintf("%s - - [%s] \"%s %s HTTP/1.1\" %d %d \"-\" \"%s\"",
		ips[rand.Intn(len(ips))],
		t.Format("02/Jan/2006:15:04:05 -0700"),
		methods[rand.Intn(len(methods))],
		paths[rand.Intn(len(paths))],
		statuses[rand.Intn(len(statuses))],
		rand.Intn(10000)+100,
		userAgents[rand.Intn(len(userAgents))],
	)
}

func generateMaskedData() MaskedData {
	return MaskedData{
		Email:      gofakeit.Email(),
		IPv4:       gofakeit.IPv4Address(),
		IPv6:       gofakeit.IPv6Address(),
		Visa:       gofakeit.CreditCardNumber(&gofakeit.CreditCardOptions{Types: []string{"visa"}}),
		Mastercard: gofakeit.CreditCardNumber(&gofakeit.CreditCardOptions{Types: []string{"mastercard"}}),
		IBAN:       generateIBAN(),
		MAC:        gofakeit.MacAddress(),
		Product:    gofakeit.ProductName(),
		Color:      gofakeit.Color(),
		Status:     generateRandomStatus(),
	}
}

func convertMaskedDataToMap(data MaskedData) map[string]string {
	return map[string]string{
		"email":      data.Email,
		"ipv4":       data.IPv4,
		"ipv6":       data.IPv6,
		"visa":       data.Visa,
		"mastercard": data.Mastercard,
		"iban":       data.IBAN,
		"mac":        data.MAC,
		"product":    data.Product,
		"color":      data.Color,
		"status":     data.Status,
	}
}

func generateIBAN() string {
	countries := []struct {
		code   string
		length int
	}{
		{"GB", 22}, {"DE", 22}, {"FR", 27}, {"IT", 27}, {"ES", 24},
		{"NL", 18}, {"BE", 16}, {"CH", 21}, {"AT", 20}, {"SE", 24},
	}

	c := countries[rand.Intn(len(countries))]
	checkDigits := fmt.Sprintf("%02d", rand.Intn(100))
	remaining := c.length - 4

	accountNumber := ""
	for range remaining {
		accountNumber += fmt.Sprintf("%d", rand.Intn(10))
	}

	return c.code + checkDigits + accountNumber
}

func generateRandomStatus() string {
	statuses := []string{"active", "pending", "completed", "processing", "failed", "success", "idle", "running"}
	return statuses[rand.Intn(len(statuses))]
}

func generateDatadogLog(t time.Time, id int64) datadogLog {
	hostnames := []string{"host-0", "host-1", "host-2", "host-3", "host-4"}
	services := []string{"web-api", "auth-service", "payment-service", "order-service", "notification-service"}
	sources := []string{"go", "python", "java", "nodejs", "nginx"}
	levels := []string{"info", "info", "info", "warn", "error", "debug"}

	level := levels[rand.Intn(len(levels))]
	msg := fmt.Sprintf("synthetic datadog log id=%d level=%s %s",
		id, level, generateNginxLog(t))

	return datadogLog{
		Message:   msg,
		Status:    level,
		Timestamp: t.UnixMilli(),
		Hostname:  hostnames[rand.Intn(len(hostnames))],
		Service:   services[rand.Intn(len(services))],
		Source:    sources[rand.Intn(len(sources))],
		Tags:      "env:local,team:edgedelta,source:loadgen",
	}
}

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
	Number          int
	ContentType     string
	Timeout         time.Duration
	TotalTime       time.Duration
	Workers         int
	MonitorPID      int
	MonitorProcess  string
	MonitorInterval time.Duration
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

	// Determine monitoring PID if monitoring is enabled.
	var processStats *ProcessStats
	if config.MonitorProcess != "" {
		pid, err := findProcessByName(config.MonitorProcess)
		if err != nil {
			log.Fatalf("Failed to find process '%s': %v", config.MonitorProcess, err)
		}
		processStats = &ProcessStats{
			pid:         pid,
			processName: config.MonitorProcess,
		}
		log.Printf("Found process '%s' with PID: %d", config.MonitorProcess, pid)
	} else if config.MonitorPID > 0 {
		processStats = &ProcessStats{
			pid: config.MonitorPID,
		}
		log.Printf("Monitoring process PID: %d", config.MonitorPID)
	}

	// Determine mode: monitoring-only vs concurrent vs normal.
	monitoringEnabled := processStats != nil
	endpointProvided := flag.Lookup("endpoint").Value.String() != flag.Lookup("endpoint").DefValue

	if monitoringEnabled && !endpointProvided {
		// Mode 2: Standalone monitoring only.
		log.Printf("Starting monitoring-only mode (interval: %s)", config.MonitorInterval)
		runMonitoringOnly(ctx, processStats, config.MonitorInterval)
		return
	}

	if monitoringEnabled {
		// Mode 1: Concurrent monitoring + load generation.
		log.Printf("Starting concurrent monitoring (interval: %s)", config.MonitorInterval)
		go monitorProcess(ctx, processStats, config.MonitorInterval)
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
	format := flag.String("format", "nginx_log", "Log format: nginx_log, apache_combined, masked_log, datadog_agent")
	number := flag.Int("number", 1, "Number of logs per worker per period")
	contentType := flag.String("content-type", "application/json", "Content-Type header")
	timeout := flag.Duration("timeout", 30*time.Second, "HTTP request timeout")
	totalTime := flag.Duration("total-time", 0, "Total time to run (0 means forever)")
	workers := flag.Int("workers", 1, "Number of concurrent workers")
	monitorPID := flag.Int("monitor-pid", 0, "PID of process to monitor (0 means disabled)")
	monitorProcess := flag.String("monitor-process", "", "Process name to monitor (e.g., 'edgedelta')")
	monitorInterval := flag.Duration("monitor-interval", 5*time.Second, "Interval for process monitoring stats")
	flag.Parse()

	return &Config{
		Endpoint:        *endpoint,
		Period:          *period,
		Format:          *format,
		Number:          *number,
		ContentType:     *contentType,
		Timeout:         *timeout,
		TotalTime:       *totalTime,
		Workers:         *workers,
		MonitorPID:      *monitorPID,
		MonitorProcess:  *monitorProcess,
		MonitorInterval: *monitorInterval,
	}
}

func run(ctx context.Context, config *Config) error {
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
		return runContinuous(ctx, client, config, stats)
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
			if err := sendLogs(ctx, client, config, stats); err != nil {
				log.Printf("Failed to send logs: %v", err)
			}
		}
	}
}

func runContinuous(ctx context.Context, client *http.Client, config *Config, stats *Stats) error {
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
					if err := sendBatch(client, config, config.Number, stats); err != nil {
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

func sendLogs(ctx context.Context, client *http.Client, config *Config, stats *Stats) error {
	if config.Workers <= 1 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return sendBatch(client, config, config.Number, stats)
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
				if err := sendBatch(client, config, config.Number, stats); err != nil {
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

// Cached payloads for maximum throughput.
var (
	cachedMaskedOnce  sync.Once
	cachedMaskedBody  []byte
	cachedMaskedErr   error
	cachedLogOnce     sync.Once
	cachedLogBody     []byte
	cachedLogErr      error
	cachedDatadogOnce sync.Once
	cachedDatadogBody []byte
	cachedDatadogErr  error
)

func sendBatch(client *http.Client, config *Config, numLogs int, stats *Stats) error {
	var body []byte
	var err error

	switch config.Format {
	case "masked_log":
		cachedMaskedOnce.Do(func() {
			batch := make([]MaskedData, numLogs)
			for i := 0; i < numLogs; i++ {
				batch[i] = generateMaskedData()
			}
			cachedMaskedBody, cachedMaskedErr = json.Marshal(batch)
		})
		if cachedMaskedErr != nil {
			return fmt.Errorf("failed to marshal masked data: %w", cachedMaskedErr)
		}
		body = cachedMaskedBody

	case "datadog_agent":
		cachedDatadogOnce.Do(func() {
			logs := make([]datadogLog, numLogs)
			for i := 0; i < numLogs; i++ {
				logs[i] = generateDatadogLog(time.Now(), int64(i))
			}
			cachedDatadogBody, cachedDatadogErr = json.Marshal(logs)
		})
		if cachedDatadogErr != nil {
			return fmt.Errorf("failed to marshal datadog logs: %w", cachedDatadogErr)
		}
		body = cachedDatadogBody

	case "apache_combined":
		cachedLogOnce.Do(func() {
			if config.ContentType == "application/json" {
				logMessages := make([]LogMessage, numLogs)
				for i := 0; i < numLogs; i++ {
					logLine := generateApacheLog(time.Now())
					maskedData := generateMaskedData()

					logMessages[i] = LogMessage{
						Timestamp: time.Now().Format(time.RFC3339),
						Message:   logLine,
						AppType:   "apache",
						PII:       convertMaskedDataToMap(maskedData),
					}
				}
				cachedLogBody, cachedLogErr = json.Marshal(logMessages)
			} else {
				var logs []string
				for i := 0; i < numLogs; i++ {
					logs = append(logs, generateApacheLog(time.Now()))
				}
				cachedLogBody = []byte(strings.Join(logs, "\n") + "\n")
			}
		})
		if cachedLogErr != nil {
			return cachedLogErr
		}
		body = cachedLogBody

	default: // nginx_log
		cachedLogOnce.Do(func() {
			if config.ContentType == "application/json" {
				logMessages := make([]LogMessage, numLogs)
				for i := 0; i < numLogs; i++ {
					logLine := generateNginxLog(time.Now())
					maskedData := generateMaskedData()

					logMessages[i] = LogMessage{
						Timestamp: time.Now().Format(time.RFC3339),
						Message:   logLine,
						AppType:   "nginx",
						PII:       convertMaskedDataToMap(maskedData),
					}
				}
				cachedLogBody, cachedLogErr = json.Marshal(logMessages)
			} else {
				var logs []string
				for i := 0; i < numLogs; i++ {
					logs = append(logs, generateNginxLog(time.Now()))
				}
				cachedLogBody = []byte(strings.Join(logs, "\n") + "\n")
			}
		})
		if cachedLogErr != nil {
			return cachedLogErr
		}
		body = cachedLogBody
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
	for i := 0; i < remaining; i++ {
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

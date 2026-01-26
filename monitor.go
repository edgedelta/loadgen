package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ProcessStats tracks process monitoring metrics.
type ProcessStats struct {
	mu          sync.Mutex
	pid         int
	cpuPercent  float64
	memoryMB    float64
	threadCount int
	processName string
}

func (ps *ProcessStats) update(cpu float64, mem float64, threads int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.cpuPercent = cpu
	ps.memoryMB = mem
	ps.threadCount = threads
}

func (ps *ProcessStats) print() {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	fmt.Printf("[MONITOR] pid: %d | cpu: %.1f%% | memory: %.1fMB | threads: %d\n",
		ps.pid, ps.cpuPercent, ps.memoryMB, ps.threadCount)
}

// findProcessByName finds a process PID by name using pgrep.
func findProcessByName(name string) (int, error) {
	cmd := exec.Command("pgrep", name)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("process not found: %s", name)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return 0, fmt.Errorf("process not found: %s", name)
	}

	pid, err := strconv.Atoi(lines[0])
	if err != nil {
		return 0, fmt.Errorf("invalid PID: %s", lines[0])
	}
	return pid, nil
}

// collectProcessMetrics collects CPU, memory, and thread metrics for a process.
func collectProcessMetrics(pid int) (cpu float64, memMB float64, threads int, err error) {
	switch runtime.GOOS {
	case "darwin":
		return collectMetricsMacOS(pid)
	case "linux":
		return collectMetricsLinux(pid)
	default:
		return 0, 0, 0, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// collectMetricsMacOS collects metrics on macOS using ps commands.
func collectMetricsMacOS(pid int) (float64, float64, int, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=,%mem=,rss=")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("process not found or terminated")
	}

	fields := strings.Fields(string(output))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected ps output format")
	}

	cpu, _ := strconv.ParseFloat(fields[0], 64)
	rssKB, _ := strconv.ParseFloat(fields[2], 64)
	memMB := rssKB / 1024

	cmd = exec.Command("sh", "-c", fmt.Sprintf("ps -M -p %d | wc -l", pid))
	output, err = cmd.Output()
	threads := 0
	if err == nil {
		count, _ := strconv.Atoi(strings.TrimSpace(string(output)))
		threads = count - 1
	}

	return cpu, memMB, threads, nil
}

// collectMetricsLinux collects metrics on Linux using ps commands.
func collectMetricsLinux(pid int) (float64, float64, int, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "%cpu=,%mem=,rss=")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("process not found or terminated")
	}

	fields := strings.Fields(string(output))
	if len(fields) < 3 {
		return 0, 0, 0, fmt.Errorf("unexpected ps output format")
	}

	cpu, _ := strconv.ParseFloat(fields[0], 64)
	rssKB, _ := strconv.ParseFloat(fields[2], 64)
	memMB := rssKB / 1024

	cmd = exec.Command("sh", "-c", fmt.Sprintf("ps -T -p %d 2>/dev/null | wc -l", pid))
	output, err = cmd.Output()
	threads := 0
	if err == nil {
		count, _ := strconv.Atoi(strings.TrimSpace(string(output)))
		threads = count - 1
	}

	return cpu, memMB, threads, nil
}

// monitorProcess runs in a goroutine to periodically collect and print process metrics.
func monitorProcess(ctx context.Context, stats *ProcessStats, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cpu, mem, threads, err := collectProcessMetrics(stats.pid)
			if err != nil {
				log.Printf("Failed to collect process metrics: %v", err)
				return
			}
			stats.update(cpu, mem, threads)
			stats.print()
		}
	}
}

// runMonitoringOnly runs monitoring as the main execution path (blocking).
func runMonitoringOnly(ctx context.Context, stats *ProcessStats, interval time.Duration) {
	log.Printf("Monitoring process PID %d (press Ctrl+C to stop)", stats.pid)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	cpu, mem, threads, err := collectProcessMetrics(stats.pid)
	if err != nil {
		log.Fatalf("Failed to collect initial metrics: %v", err)
	}
	stats.update(cpu, mem, threads)
	stats.print()

	for {
		select {
		case <-ctx.Done():
			log.Println("Monitoring stopped")
			return
		case <-ticker.C:
			cpu, mem, threads, err := collectProcessMetrics(stats.pid)
			if err != nil {
				log.Printf("Process terminated or monitoring failed: %v", err)
				return
			}
			stats.update(cpu, mem, threads)
			stats.print()
		}
	}
}

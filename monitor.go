package main

import (
	"context"
	"fmt"
	"log"
	"os"
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
// sampleInterval specifies how long to wait between CPU samples for calculating instantaneous CPU.
func collectProcessMetrics(pid int, sampleInterval time.Duration) (cpu float64, memMB float64, threads int, err error) {
	switch runtime.GOOS {
	case "darwin":
		return collectMetricsMacOS(pid, sampleInterval)
	case "linux":
		return collectMetricsLinux(pid, sampleInterval)
	default:
		return 0, 0, 0, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// cpuTimeMacOS returns the total CPU time in seconds for a process.
func cpuTimeMacOS(pid int) (float64, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "time=")
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("process not found or terminated")
	}

	// Output format: "MM:SS.ss" or "HH:MM:SS"
	timeStr := strings.TrimSpace(string(output))
	parts := strings.Split(timeStr, ":")

	var totalSeconds float64
	if len(parts) == 2 {
		// MM:SS.ss format
		mins, _ := strconv.ParseFloat(parts[0], 64)
		secs, _ := strconv.ParseFloat(parts[1], 64)
		totalSeconds = mins*60 + secs
	} else if len(parts) == 3 {
		// HH:MM:SS format
		hours, _ := strconv.ParseFloat(parts[0], 64)
		mins, _ := strconv.ParseFloat(parts[1], 64)
		secs, _ := strconv.ParseFloat(parts[2], 64)
		totalSeconds = hours*3600 + mins*60 + secs
	}

	return totalSeconds, nil
}

// collectMetricsMacOS collects metrics on macOS using ps commands.
func collectMetricsMacOS(pid int, sampleInterval time.Duration) (float64, float64, int, error) {
	// Get first CPU time sample
	cpuTime1, err := cpuTimeMacOS(pid)
	if err != nil {
		return 0, 0, 0, err
	}

	time1 := time.Now()

	// Wait for sampling interval
	time.Sleep(sampleInterval)

	// Get second CPU time sample
	cpuTime2, err := cpuTimeMacOS(pid)
	if err != nil {
		return 0, 0, 0, err
	}

	time2 := time.Now()

	// Calculate instantaneous CPU percentage
	cpuDelta := cpuTime2 - cpuTime1
	timeDelta := time2.Sub(time1).Seconds()
	cpuPercent := (cpuDelta / timeDelta) * 100.0

	// Get memory (RSS)
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "rss=")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("process not found or terminated")
	}

	rssKB, _ := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	memMB := rssKB / 1024

	// Get thread count
	cmd = exec.Command("sh", "-c", fmt.Sprintf("ps -M -p %d | wc -l", pid))
	output, err = cmd.Output()
	threads := 0
	if err == nil {
		count, _ := strconv.Atoi(strings.TrimSpace(string(output)))
		threads = count - 1
	}

	return cpuPercent, memMB, threads, nil
}

// cpuTimeLinux returns the total CPU time in clock ticks for a process.
func cpuTimeLinux(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("process not found or terminated")
	}

	// Parse /proc/[pid]/stat
	// Format: pid (comm) state ppid ... utime stime cutime cstime ...
	// We need utime (field 14) and stime (field 15)
	fields := strings.Fields(string(data))
	if len(fields) < 15 {
		return 0, fmt.Errorf("unexpected /proc/stat format")
	}

	utime, _ := strconv.ParseUint(fields[13], 10, 64)
	stime, _ := strconv.ParseUint(fields[14], 10, 64)

	return utime + stime, nil
}

// collectMetricsLinux collects metrics on Linux using /proc filesystem.
func collectMetricsLinux(pid int, sampleInterval time.Duration) (float64, float64, int, error) {
	// Get clock ticks per second (usually 100)
	clkTck := uint64(100) // Default value
	cmd := exec.Command("getconf", "CLK_TCK")
	if output, err := cmd.Output(); err == nil {
		if tck, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64); err == nil {
			clkTck = tck
		}
	}

	// Get first CPU time sample
	cpuTicks1, err := cpuTimeLinux(pid)
	if err != nil {
		return 0, 0, 0, err
	}

	time1 := time.Now()

	// Wait for sampling interval
	time.Sleep(sampleInterval)

	// Get second CPU time sample
	cpuTicks2, err := cpuTimeLinux(pid)
	if err != nil {
		return 0, 0, 0, err
	}

	time2 := time.Now()

	// Calculate instantaneous CPU percentage
	ticksDelta := cpuTicks2 - cpuTicks1
	timeDelta := time2.Sub(time1).Seconds()
	cpuSeconds := float64(ticksDelta) / float64(clkTck)
	cpuPercent := (cpuSeconds / timeDelta) * 100.0

	// Get memory (RSS)
	cmd = exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "rss=")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("process not found or terminated")
	}

	rssKB, _ := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	memMB := rssKB / 1024

	// Get thread count
	cmd = exec.Command("sh", "-c", fmt.Sprintf("ps -T -p %d 2>/dev/null | wc -l", pid))
	output, err = cmd.Output()
	threads := 0
	if err == nil {
		count, _ := strconv.Atoi(strings.TrimSpace(string(output)))
		threads = count - 1
	}

	return cpuPercent, memMB, threads, nil
}

// calculateSampleInterval returns a reasonable CPU sampling interval based on the monitoring interval.
// Uses 20% of monitoring interval, capped between 100ms and 2s for accuracy and performance.
func calculateSampleInterval(monitorInterval time.Duration) time.Duration {
	sampleInterval := monitorInterval / 5 // 20% of monitoring interval

	// Cap between 100ms and 2s
	if sampleInterval < 100*time.Millisecond {
		return 100 * time.Millisecond
	}
	if sampleInterval > 2*time.Second {
		return 2 * time.Second
	}
	return sampleInterval
}

// monitorProcess runs in a goroutine to periodically collect and print process metrics.
func monitorProcess(ctx context.Context, stats *ProcessStats, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	sampleInterval := calculateSampleInterval(interval)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cpu, mem, threads, err := collectProcessMetrics(stats.pid, sampleInterval)
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

	sampleInterval := calculateSampleInterval(interval)

	cpu, mem, threads, err := collectProcessMetrics(stats.pid, sampleInterval)
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
			cpu, mem, threads, err := collectProcessMetrics(stats.pid, sampleInterval)
			if err != nil {
				log.Printf("Process terminated or monitoring failed: %v", err)
				return
			}
			stats.update(cpu, mem, threads)
			stats.print()
		}
	}
}

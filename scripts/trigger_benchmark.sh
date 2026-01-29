#!/bin/bash

set -e

app=$1

if [[ -z "$app" ]]; then
  echo "Select app for benchmark:"
  select app in edgedelta observiq cribl; do
    if [[ -n "$app" ]]; then
      break
    fi
    echo "Invalid selection. Try again."
  done
fi

timestamp=$(date +%Y%m%d_%H%M%S)
log_dir="benchmark_results"
mkdir -p "$log_dir"

# Verify run_bench.sh exists and is executable
if [[ ! -x "./run_benchmark.sh" ]]; then
  echo "Error: run_benchmark.sh not found or not executable"
  exit 1
fi

echo "========================================="
echo "Running benchmark for $app"
echo "========================================="
  
log_file="$log_dir/${app}_${timestamp}.log"

if ./run_benchmark.sh "$app" | tee "$log_file"; then
  echo "Benchmark for $app completed successfully"
else
  echo "Error: Benchmark for $app failed (check $log_file for details)"
  exit 1
fi

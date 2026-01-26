#!/bin/bash
# Monitor a process while generating load
# This demonstrates how process metrics change under load

PROCESS_NAME=${1:-edgedelta}

../httploggen \
    --endpoint ${ENDPOINT:-http://localhost:8085} \
    --format nginx_log \
    --number 10 \
    --workers 48 \
    --period 10ms \
    --total-time 60s \
    --monitor-process "$PROCESS_NAME" \
    --monitor-interval 5s

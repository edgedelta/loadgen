#!/bin/bash
# Monitor a process without generating load
# Usage: ./monitor-only.sh [process-name or --monitor-pid PID]

PROCESS_NAME=${1:-edgedelta}

if [[ "$1" == "--monitor-pid" ]]; then
    ../httploggen \
        --monitor-pid $2 \
        --monitor-interval 5s
else
    ../httploggen \
        --monitor-process "$PROCESS_NAME" \
        --monitor-interval 5s
fi

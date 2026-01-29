#!/bin/bash
# Basic load test - 1000 logs/sec for 30 seconds

../loadgen \
    --endpoint ${ENDPOINT:-http://localhost:8085} \
    --format nginx_log \
    --number 10 \
    --workers 10 \
    --period 100ms \
    --total-time 30s

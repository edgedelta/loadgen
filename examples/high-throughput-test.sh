#!/bin/bash
# High throughput test - ~48K logs/sec for 1 minute
# This is the test used in our blog post comparing log collectors

../loadgen \
    --endpoint ${ENDPOINT:-http://localhost:8085} \
    --format nginx_log \
    --number 1 \
    --workers 120 \
    --period 1ms \
    --total-time 1m

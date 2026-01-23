#!/bin/bash
# Maximum throughput test - continuous mode
# Workers send requests as fast as possible without waiting

../httploggen \
    --endpoint ${ENDPOINT:-http://localhost:8085} \
    --format nginx_log \
    --number 100 \
    --workers ${WORKERS:-100} \
    --period 0 \
    --total-time ${DURATION:-1m}

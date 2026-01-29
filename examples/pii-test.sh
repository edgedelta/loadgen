#!/bin/bash
# Test PII masking/redaction pipeline
# Sends pure PII data without log wrappers

../loadgen \
    --endpoint ${ENDPOINT:-http://localhost:8085} \
    --format masked_log \
    --number 100 \
    --workers 5 \
    --period 1s \
    --total-time 30s

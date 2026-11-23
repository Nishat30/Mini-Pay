#!/usr/bin/env bash
# demo.sh — End-to-end MiniPay Phase 1 Demonstration script
set -euo pipefail

echo "=========================================================="
echo "             MINIPAY PHASE 1 DEMONSTRATION                "
echo "=========================================================="

echo -e "\n[1/6] Applying database migrations..."
go run ./cmd/minipay migrate up

echo -e "\n[2/6] Provisioning Demo Merchant..."
MERCHANT_OUT=$(go run ./cmd/minipay admin create-merchant --name "Acme Demo Corp")
echo "$MERCHANT_OUT"

API_KEY=$(echo "$MERCHANT_OUT" | grep "API Key:" | awk '{print $3}')
echo "Captured API Key: $API_KEY"

echo -e "\n[3/6] Starting MiniPay API server in background..."
go run ./cmd/minipay api &
API_PID=$!
trap "kill -9 $API_PID 2>/dev/null || true" EXIT
sleep 3

IDEMP_KEY="demo-key-$(date +%s%N)"
BODY='{"amount":1000,"currency":"usd","metadata":{"order_id":"demo_001"}}'

echo -e "\n[4/6] Sending initial POST /v1/payments with Idempotency-Key..."
curl -i -s -X POST "http://localhost:8080/v1/payments" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $IDEMP_KEY" \
  -H "Content-Type: application/json" \
  -d "$BODY"

echo -e "\n\n[5/6] Retrying identical POST with the same Idempotency-Key (Replay Test)..."
curl -i -s -X POST "http://localhost:8080/v1/payments" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Idempotency-Key: $IDEMP_KEY" \
  -H "Content-Type: application/json" \
  -d "$BODY"

echo -e "\n\n[6/6] Verifying Ledger Invariants via Admin CLI..."
go run ./cmd/minipay admin check-ledger

echo -e "\n=========================================================="
echo "        ALL PHASE 1 DEMO CHECKS COMPLETED SUCCESSFULLY     "
echo "=========================================================="

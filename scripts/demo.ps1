# demo.ps1 — End-to-end MiniPay Phase 1 Demonstration script for Windows PowerShell
$ErrorActionPreference = "Stop"

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "             MINIPAY PHASE 1 DEMONSTRATION                " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

# 1. Run Migrations
Write-Host "`n[1/6] Applying database migrations..." -ForegroundColor Yellow
go run ./cmd/minipay migrate up

# 2. Create Merchant
Write-Host "`n[2/6] Provisioning Demo Merchant..." -ForegroundColor Yellow
$merchantOutput = go run ./cmd/minipay admin create-merchant --name "Acme Demo Corp"
Write-Host $merchantOutput

# Extract API key
$apiKey = ($merchantOutput | Select-String -Pattern "API Key:\s+(\S+)").Matches.Groups[1].Value
Write-Host "Captured API Key: $apiKey" -ForegroundColor Green

# 3. Start API Server in background
Write-Host "`n[3/6] Starting MiniPay API server in background..." -ForegroundColor Yellow
$apiProcess = Start-Process -FilePath "go" -ArgumentList "run", "./cmd/minipay", "api" -PassThru
Start-Sleep -Seconds 3

try {
    # 4. First Payment Request
    Write-Host "`n[4/6] Sending initial POST /v1/payments with Idempotency-Key..." -ForegroundColor Yellow
    $idempotencyKey = "demo-key-" + [System.Guid]::NewGuid().ToString()
    $body = '{"amount":1000,"currency":"usd","metadata":{"order_id":"demo_001"}}'

    $headers = @{
        "Authorization"   = "Bearer $apiKey"
        "Idempotency-Key" = $idempotencyKey
        "Content-Type"    = "application/json"
    }

    $resp1 = Invoke-WebRequest -Uri "http://localhost:8080/v1/payments" -Method Post -Headers $headers -Body $body -UseBasicParsing
    Write-Host "Response Status: $($resp1.StatusCode)" -ForegroundColor Green
    Write-Host "Response Body:   $($resp1.Content)" -ForegroundColor Green
    Write-Host "Idempotent-Replayed Header: '$($resp1.Headers['Idempotent-Replayed'])'"

    # 5. Duplicate Payment Request (Replay test)
    Write-Host "`n[5/6] Retrying identical POST with the same Idempotency-Key (Replay Test)..." -ForegroundColor Yellow
    $resp2 = Invoke-WebRequest -Uri "http://localhost:8080/v1/payments" -Method Post -Headers $headers -Body $body -UseBasicParsing
    Write-Host "Response Status: $($resp2.StatusCode)" -ForegroundColor Green
    Write-Host "Response Body:   $($resp2.Content)" -ForegroundColor Green
    Write-Host "Idempotent-Replayed Header: '$($resp2.Headers['Idempotent-Replayed'])'" -ForegroundColor Cyan

    if ($resp2.Headers['Idempotent-Replayed'] -ne "true") {
        Write-Error "Expected Idempotent-Replayed: true header on duplicate request!"
    } else {
        Write-Host "SUCCESS: Request was correctly detected as idempotent replay!" -ForegroundColor Green
    }

    # 6. Run Worker & Verify Ledger
    Write-Host "`n[6/6] Verifying Ledger Invariants via Admin CLI..." -ForegroundColor Yellow
    go run ./cmd/minipay admin check-ledger

    Write-Host "`n==========================================================" -ForegroundColor Cyan
    Write-Host "        ALL PHASE 1 DEMO CHECKS COMPLETED SUCCESSFULLY     " -ForegroundColor Cyan
    Write-Host "==========================================================" -ForegroundColor Cyan
}
finally {
    if ($apiProcess -and -not $apiProcess.HasExited) {
        Stop-Process -Id $apiProcess.Id -Force
    }
}

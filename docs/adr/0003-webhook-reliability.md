# ADR 0003: Webhook Reliability, HMAC Signatures, and SSRF Prevention

## Status

Accepted (2026-11-23)

## Context

Payments platforms operate asynchronously: once a customer payment is authorized by an external payment processor or simulator, the platform must notify the merchant's backend system via HTTP webhooks.

Designing a production webhook delivery architecture presents three critical operational and security challenges:

1. **Security & Server-Side Request Forgery (SSRF)**:
   Allowing merchants to register arbitrary webhook URLs introduces catastrophic SSRF vulnerabilities if the delivery worker can be coerced into reaching internal microservices, AWS/GCP instance metadata endpoints (`169.254.169.254`), loopback listeners (`127.0.0.1`), or private subnets (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`). Furthermore, simple static IP checks are vulnerable to **DNS rebinding**, where an attacker's domain initially resolves to a public IP to pass validation, but resolves to a private IP during connection establishment.

2. **Authenticity & Replay Protection**:
   Merchants must have cryptographic certainty that incoming webhook payloads originate from MiniPay, have not been tampered with in transit, and are not replay attacks intercepted from historical network captures.

3. **Reliability & Backoff**:
   Merchant webhooks will inevitably experience transient outages, timeouts, and network degradation. The system must retry with exponential backoff and protect itself from infinite retry loops via dead-lettering.

## Decision

We implemented a defense-in-depth webhook architecture governed by four core mechanisms:

### 1. Two-Phase Socket Control & SSRF Defense

SSRF protection is implemented at both static configuration time and socket dialing time:
- **Static Validation**: URLs must use the `https://` scheme (HTTP is disallowed outside explicit dev testing flags), must not contain user credentials (`user:pass@host`), and must not specify direct IP addresses belonging to blocked CIDRs.
- **Dial-Time DNS Pre-Resolution & Socket Control**: MiniPay constructs an isolated `http.Client` with custom transport controls:
  ```go
  Control: func(network, address string, c syscall.RawConn) error {
      host, _, _ := net.SplitHostPort(address)
      if ip := net.ParseIP(host); ip != nil && IsPrivateIP(ip) {
          return ErrPrivateIP
      }
      return nil
  }
  ```
  Before connecting, all resolved IP addresses are evaluated against 15+ prohibited CIDR blocks (including RFC 1918, CGNAT `100.64.0.0/10`, link-local `169.254.0.0/16`, and IPv6 unique local `fc00::/7`). This mitigates DNS rebinding attacks completely.

### 2. HMAC-SHA256 Payload Signing

Every webhook request contains the header:
```http
MiniPay-Signature: t=1700000000,v1=52571869e7922dc135...
```
Where:
- $t$ is the Unix timestamp in seconds.
- The signed payload is: $\text{Payload} = t \parallel \text{"."} \parallel \text{Body}$.
- The signature is: $v1 = \text{hex}(\text{HMAC-SHA256}(\text{merchant.webhook\_secret}, \text{Payload}))$.
- Replay prevention: Merchants verify the signature using constant-time comparison (`hmac.Equal`) and reject timestamps older than 5 minutes (`DefaultSignatureTolerance`).

### 3. Asynchronous Outbox Decoupling

Payment transactions never perform synchronous outbound HTTP calls. Instead, the payment worker writes event records (`payment.succeeded`, `payment.failed`) to `outbox_events`. The webhook dispatcher reads these events asynchronously, records a tracking row in `webhook_deliveries`, and manages independent delivery lifecycles.

### 4. Exponential Backoff and Dead-Lettering

- **Timeout**: Each HTTP dispatch has a strict 5-second request timeout.
- **Retry Schedule**: If the merchant returns non-2xx status codes or times out, the next attempt is scheduled with exponential backoff:
  $$\text{NextAttempt} = \text{Now} + \left( \text{InitialBackoff} \times 2^{\text{attempts}-1} \right)$$
- **Dead-Letter Archive**: After 5 failed attempts (`attempts >= 5`), the delivery transitions from `state = 'pending'` to `state = 'dead'`, recording `last_status` and `last_error` for auditing, and marks the parent outbox event processed so system queues do not stall.

## Consequences

### Positive
- Total protection against SSRF, cloud credential theft, and DNS rebinding attacks.
- Robust cryptographic authenticity for merchants with zero shared symmetric secrets between different merchants.
- High resilience: temporary merchant outages do not degrade payment authorization throughput or stall outbox workers.
- Complete auditability through the `webhook_deliveries` table.

### Negative / Trade-offs
- Merchants must host public HTTPS servers (or use tunneling tools like ngrok in development) to receive webhooks.
- Backlogged dead deliveries consume database storage until archived or pruned by retention cleanup routines.

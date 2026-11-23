package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/minipay/minipay/internal/platform"
)

const (
	// DefaultSignatureTolerance is the default window before a signature is considered expired.
	DefaultSignatureTolerance = 5 * time.Minute
	SignatureHeader           = "MiniPay-Signature"
)

var (
	ErrInvalidSignatureHeader = errors.New("malformed or missing signature header")
	ErrSignatureExpired       = errors.New("webhook signature timestamp is outside the allowed tolerance window")
	ErrInvalidSignature       = errors.New("webhook HMAC signature mismatch")
)

// SignPayload generates the MiniPay-Signature header value:
// t=<timestamp>,v1=<hex_hmac_sha256>
func SignPayload(body []byte, secret string, timestamp int64) string {
	payload := fmt.Sprintf("%d.%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("t=%d,v1=%s", timestamp, signature)
}

// VerifySignature validates a webhook signature header against the payload, secret, and clock.
func VerifySignature(body []byte, header string, secret string, tolerance time.Duration, clock platform.Clock) error {
	if header == "" {
		return ErrInvalidSignatureHeader
	}
	if clock == nil {
		clock = platform.RealClock{}
	}

	var (
		timestampStr string
		signatureHex string
	)

	elements := strings.Split(header, ",")
	for _, element := range elements {
		parts := strings.SplitN(strings.TrimSpace(element), "=", 2)
		if len(parts) == 2 {
			switch parts[0] {
			case "t":
				timestampStr = parts[1]
			case "v1":
				signatureHex = parts[1]
			}
		}
	}

	if timestampStr == "" || signatureHex == "" {
		return ErrInvalidSignatureHeader
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return ErrInvalidSignatureHeader
	}

	// Replay protection check
	now := clock.Now().Unix()
	diff := now - timestamp
	if diff < 0 {
		diff = -diff
	}
	if tolerance > 0 && time.Duration(diff)*time.Second > tolerance {
		return ErrSignatureExpired
	}

	// Verify HMAC in constant time
	payload := fmt.Sprintf("%d.%s", timestamp, string(body))
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	expectedMAC := mac.Sum(nil)

	actualMAC, err := hex.DecodeString(signatureHex)
	if err != nil {
		return ErrInvalidSignature
	}

	if !hmac.Equal(expectedMAC, actualMAC) {
		return ErrInvalidSignature
	}

	return nil
}

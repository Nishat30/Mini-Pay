// ids.go provides prefixed ULID generation for public IDs.
// Owns: generating collision-resistant, sortable identifiers (mer_, pay_, ref_, etc.).
// Must never: generate unprefixed IDs for public entities.
// Relies on: crypto/rand entropy source and oklog/ulid.
package platform

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	entropyMu sync.Mutex
	entropy   = ulid.Monotonic(rand.Reader, 0)
)

// IDPrefix defines recognized prefixes for MiniPay entities.
type IDPrefix string

const (
	PrefixMerchant           IDPrefix = "mer_"
	PrefixPayment            IDPrefix = "pay_"
	PrefixRefund             IDPrefix = "ref_"
	PrefixLedgerAccount      IDPrefix = "acc_"
	PrefixLedgerTransaction  IDPrefix = "ltx_"
	PrefixWebhookDelivery    IDPrefix = "whd_"
	PrefixEvent              IDPrefix = "evt_"
	PrefixRequest            IDPrefix = "req_"
)

// NewID generates a new prefixed ULID (e.g. pay_01H...).
// Uses monotonic entropy source protected by a mutex for thread safety.
func NewID(prefix IDPrefix) string {
	entropyMu.Lock()
	defer entropyMu.Unlock()

	id, err := ulid.New(ulid.Timestamp(time.Now()), entropy)
	if err != nil {
		// Fallback to non-monotonic crypto/rand if monotonic fails
		id = ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader)
	}
	return fmt.Sprintf("%s%s", prefix, id.String())
}

// ValidateID checks if an ID starts with the expected prefix and contains a valid ULID.
func ValidateID(prefix IDPrefix, id string) bool {
	if len(id) <= len(prefix) {
		return false
	}
	if id[:len(prefix)] != string(prefix) {
		return false
	}
	_, err := ulid.Parse(id[len(prefix):])
	return err == nil
}

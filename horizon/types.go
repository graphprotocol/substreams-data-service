package horizon

import (
	"crypto/rand"
	"encoding/json"
	"math/big"
	"time"

	"github.com/streamingfast/eth-go"
)

// CollectionID is a 32-byte identifier for a collection (derived from allocation)
type CollectionID [32]byte

// MarshalJSON implements json.Marshaler
func (c CollectionID) MarshalJSON() ([]byte, error) {
	return json.Marshal(eth.Hash(c[:]).Pretty())
}

// UnmarshalJSON implements json.Unmarshaler
func (c *CollectionID) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	h := eth.MustNewHash(s)
	copy(c[:], h)
	return nil
}

// Receipt represents a V2 TAP receipt (Horizon - collection-based)
type Receipt struct {
	CollectionID    CollectionID `json:"collection_id"`
	Payer           eth.Address  `json:"payer"`
	DataService     eth.Address  `json:"data_service"`
	ServiceProvider eth.Address  `json:"service_provider"`
	TimestampNs     uint64       `json:"timestamp_ns"`
	Nonce           uint64       `json:"nonce"`
	Value           *big.Int     `json:"value"`
}

// NewReceipt creates a new receipt with current timestamp and random nonce
func NewReceipt(
	collectionID CollectionID,
	payer, dataService, serviceProvider eth.Address,
	value *big.Int,
) *Receipt {
	return &Receipt{
		CollectionID:    collectionID,
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     uint64(time.Now().UnixNano()),
		Nonce:           randomUint64(),
		Value:           new(big.Int).Set(value),
	}
}

// RAV represents a V2 Receipt Aggregate Voucher (Horizon)
type RAV struct {
	CollectionID    CollectionID `json:"collectionId"`
	Payer           eth.Address  `json:"payer"`
	ServiceProvider eth.Address  `json:"serviceProvider"`
	DataService     eth.Address  `json:"dataService"`
	TimestampNs     uint64       `json:"timestampNs"`
	ValueAggregate  *big.Int     `json:"valueAggregate"`
	Metadata        []byte       `json:"metadata"`
}

// MaxUint128 is the maximum value for uint128
var MaxUint128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 128), big.NewInt(1))

// randomUint64 generates a random uint64 for nonce
func randomUint64() uint64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
		uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
}

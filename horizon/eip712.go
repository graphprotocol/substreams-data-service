package horizon

import (
	"encoding/binary"
	"math/big"

	"github.com/streamingfast/eth-go"
)

// EIP712Encodable is implemented by types that can be EIP-712 encoded
type EIP712Encodable interface {
	EIP712TypeHash() eth.Hash
	EIP712EncodeData() []byte
}

// Domain represents an EIP-712 domain separator for V2 (Horizon)
type Domain struct {
	Name              string
	Version           string
	ChainID           *big.Int
	VerifyingContract eth.Address
}

// EIP712 type hashes (pre-computed)
var (
	eip712DomainTypeHash = keccak256([]byte(
		"EIP712Domain(string name,string version,uint256 chainId,address verifyingContract)"))

	receiptTypeHash = keccak256([]byte(
		"Receipt(bytes32 collection_id,address payer,address data_service,address service_provider,uint64 timestamp_ns,uint64 nonce,uint128 value)"))

	ravTypeHash = keccak256([]byte(
		"ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)"))
)

// NewDomain creates a V2 Horizon EIP-712 domain
func NewDomain(chainID uint64, verifyingContract eth.Address) *Domain {
	return &Domain{
		Name:              "GraphTallyCollector",
		Version:           "1",
		ChainID:           big.NewInt(int64(chainID)),
		VerifyingContract: verifyingContract,
	}
}

// Separator computes the EIP-712 domain separator hash
func (d *Domain) Separator() eth.Hash {
	encoded := make([]byte, 0, 32*5)
	encoded = append(encoded, eip712DomainTypeHash[:]...)
	encoded = append(encoded, keccak256([]byte(d.Name))[:]...)
	encoded = append(encoded, keccak256([]byte(d.Version))[:]...)
	encoded = append(encoded, padLeft(d.ChainID.Bytes(), 32)...)
	encoded = append(encoded, padLeft(d.VerifyingContract[:], 32)...)

	return keccak256(encoded)
}

// EIP712TypeHash returns the type hash for Receipt
func (r *Receipt) EIP712TypeHash() eth.Hash {
	return receiptTypeHash
}

// EIP712EncodeData returns the ABI-encoded data for Receipt
func (r *Receipt) EIP712EncodeData() []byte {
	encoded := make([]byte, 0, 32*7)
	encoded = append(encoded, r.CollectionID[:]...)                 // bytes32
	encoded = append(encoded, padLeft(r.Payer[:], 32)...)           // address
	encoded = append(encoded, padLeft(r.DataService[:], 32)...)     // address
	encoded = append(encoded, padLeft(r.ServiceProvider[:], 32)...) // address
	encoded = append(encoded, encodeUint64(r.TimestampNs)...)       // uint64
	encoded = append(encoded, encodeUint64(r.Nonce)...)             // uint64
	encoded = append(encoded, encodeUint128(r.Value)...)            // uint128
	return encoded
}

// EIP712TypeHash returns the type hash for RAV
func (r *RAV) EIP712TypeHash() eth.Hash {
	return ravTypeHash
}

// EIP712EncodeData returns the ABI-encoded data for RAV
func (r *RAV) EIP712EncodeData() []byte {
	encoded := make([]byte, 0, 32*7)
	encoded = append(encoded, r.CollectionID[:]...)                 // bytes32
	encoded = append(encoded, padLeft(r.Payer[:], 32)...)           // address
	encoded = append(encoded, padLeft(r.ServiceProvider[:], 32)...) // address
	encoded = append(encoded, padLeft(r.DataService[:], 32)...)     // address
	encoded = append(encoded, encodeUint64(r.TimestampNs)...)       // uint64
	encoded = append(encoded, encodeUint128(r.ValueAggregate)...)   // uint128
	encoded = append(encoded, keccak256(r.Metadata)[:]...)          // keccak256(bytes)
	return encoded
}

// HashTypedData computes the EIP-712 hash for signing
// Returns: keccak256("\x19\x01" || domainSeparator || structHash)
func HashTypedData[T EIP712Encodable](domain *Domain, message T) (eth.Hash, error) {
	structHash := hashStruct(message)
	domainSep := domain.Separator()

	// EIP-712: "\x19\x01" || domainSeparator || structHash
	data := make([]byte, 0, 2+32+32)
	data = append(data, 0x19, 0x01)
	data = append(data, domainSep[:]...)
	data = append(data, structHash[:]...)

	return keccak256(data), nil
}

// hashStruct computes keccak256(typeHash || encodeData)
func hashStruct[T EIP712Encodable](message T) eth.Hash {
	typeHash := message.EIP712TypeHash()
	encodedData := message.EIP712EncodeData()

	data := make([]byte, 0, 32+len(encodedData))
	data = append(data, typeHash[:]...)
	data = append(data, encodedData...)

	return keccak256(data)
}

// Helper functions

func keccak256(data []byte) eth.Hash {
	return eth.Keccak256(data)
}

func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b[len(b)-size:]
	}
	result := make([]byte, size)
	copy(result[size-len(b):], b)
	return result
}

func encodeUint64(v uint64) []byte {
	result := make([]byte, 32)
	binary.BigEndian.PutUint64(result[24:], v)
	return result
}

func encodeUint128(v *big.Int) []byte {
	result := make([]byte, 32)
	if v != nil {
		b := v.Bytes()
		copy(result[32-len(b):], b)
	}
	return result
}

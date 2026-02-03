package horizon

import (
	"errors"
	"math/big"

	"github.com/streamingfast/eth-go"
)

var (
	ErrNoReceipts              = errors.New("no valid receipts for RAV request")
	ErrAggregateOverflow       = errors.New("aggregating receipt results in overflow")
	ErrDuplicateSignature      = errors.New("duplicate receipt signature detected")
	ErrInvalidTimestamp        = errors.New("receipt timestamp not greater than previous RAV")
	ErrCollectionMismatch      = errors.New("receipts have different collection IDs")
	ErrPayerMismatch           = errors.New("receipts have different payer addresses")
	ErrServiceProviderMismatch = errors.New("receipts have different service provider addresses")
	ErrDataServiceMismatch     = errors.New("receipts have different data service addresses")
	ErrInvalidSigner           = errors.New("receipt signed by unauthorized signer")
	ErrRAVSignerMismatch       = errors.New("previous RAV signed by unauthorized signer")
)

// Aggregator handles receipt validation and RAV generation
type Aggregator struct {
	domain          *Domain
	signerKey       *eth.PrivateKey
	acceptedSigners map[string]bool
}

// NewAggregator creates a new RAV aggregator
func NewAggregator(domain *Domain, signerKey *eth.PrivateKey, acceptedSigners []eth.Address) *Aggregator {
	signerMap := make(map[string]bool, len(acceptedSigners))
	for _, addr := range acceptedSigners {
		signerMap[addr.Pretty()] = true
	}

	return &Aggregator{
		domain:          domain,
		signerKey:       signerKey,
		acceptedSigners: signerMap,
	}
}

// AggregateReceipts validates receipts and creates a signed RAV
func (a *Aggregator) AggregateReceipts(
	receipts []*SignedReceipt,
	previousRAV *SignedRAV,
) (*SignedRAV, error) {

	if len(receipts) == 0 {
		return nil, ErrNoReceipts
	}

	// Validate signatures are unique (malleability protection)
	if err := a.checkSignaturesUnique(receipts); err != nil {
		return nil, err
	}

	// Verify all receipts are from accepted signers
	if err := a.verifyReceiptSigners(receipts); err != nil {
		return nil, err
	}

	// Verify previous RAV signer if present
	if previousRAV != nil {
		if err := a.verifyRAVSigner(previousRAV); err != nil {
			return nil, err
		}
	}

	// Check receipt timestamps are after previous RAV
	if err := checkReceiptTimestamps(receipts, previousRAV); err != nil {
		return nil, err
	}

	// Validate field consistency across all receipts
	if err := validateReceiptConsistency(receipts); err != nil {
		return nil, err
	}

	// Verify previous RAV fields match receipts
	if previousRAV != nil {
		if err := validateRAVConsistency(receipts[0].Message, previousRAV.Message); err != nil {
			return nil, err
		}
	}

	// Perform aggregation
	rav, err := aggregate(receipts, previousRAV)
	if err != nil {
		return nil, err
	}

	// Sign and return
	return Sign(a.domain, rav, a.signerKey)
}

// aggregate creates a RAV from validated receipts
func aggregate(receipts []*SignedReceipt, previousRAV *SignedRAV) (*RAV, error) {
	first := receipts[0].Message

	var timestampMax uint64 = 0
	valueAggregate := big.NewInt(0)

	// Initialize from previous RAV if present
	if previousRAV != nil {
		timestampMax = previousRAV.Message.TimestampNs
		valueAggregate = new(big.Int).Set(previousRAV.Message.ValueAggregate)
	}

	// Aggregate all receipts
	for _, r := range receipts {
		receipt := r.Message

		// Add value with overflow check
		newValue := new(big.Int).Add(valueAggregate, receipt.Value)
		if newValue.Cmp(MaxUint128) > 0 {
			return nil, ErrAggregateOverflow
		}
		valueAggregate = newValue

		// Track max timestamp
		if receipt.TimestampNs > timestampMax {
			timestampMax = receipt.TimestampNs
		}
	}

	return &RAV{
		CollectionID:    first.CollectionID,
		Payer:           first.Payer,
		ServiceProvider: first.ServiceProvider,
		DataService:     first.DataService,
		TimestampNs:     timestampMax,
		ValueAggregate:  valueAggregate,
		Metadata:        []byte{}, // Empty metadata by default
	}, nil
}

func (a *Aggregator) checkSignaturesUnique(receipts []*SignedReceipt) error {
	seen := make(map[[65]byte]bool, len(receipts))
	for _, r := range receipts {
		normalized := normalizeSignature(r.Signature)
		if seen[normalized] {
			return ErrDuplicateSignature
		}
		seen[normalized] = true
	}
	return nil
}

func (a *Aggregator) verifyReceiptSigners(receipts []*SignedReceipt) error {
	for _, r := range receipts {
		signer, err := r.RecoverSigner(a.domain)
		if err != nil {
			return err
		}
		if !a.acceptedSigners[signer.Pretty()] {
			return ErrInvalidSigner
		}
	}
	return nil
}

func (a *Aggregator) verifyRAVSigner(rav *SignedRAV) error {
	signer, err := rav.RecoverSigner(a.domain)
	if err != nil {
		return err
	}
	if !a.acceptedSigners[signer.Pretty()] {
		return ErrRAVSignerMismatch
	}
	return nil
}

func checkReceiptTimestamps(receipts []*SignedReceipt, previousRAV *SignedRAV) error {
	if previousRAV == nil {
		return nil
	}
	ravTimestamp := previousRAV.Message.TimestampNs
	for _, r := range receipts {
		if r.Message.TimestampNs <= ravTimestamp {
			return ErrInvalidTimestamp
		}
	}
	return nil
}

func validateReceiptConsistency(receipts []*SignedReceipt) error {
	if len(receipts) == 0 {
		return nil
	}

	first := receipts[0].Message
	for _, r := range receipts[1:] {
		if r.Message.CollectionID != first.CollectionID {
			return ErrCollectionMismatch
		}
		if !addressesEqual(r.Message.Payer, first.Payer) {
			return ErrPayerMismatch
		}
		if !addressesEqual(r.Message.ServiceProvider, first.ServiceProvider) {
			return ErrServiceProviderMismatch
		}
		if !addressesEqual(r.Message.DataService, first.DataService) {
			return ErrDataServiceMismatch
		}
	}
	return nil
}

func validateRAVConsistency(receipt *Receipt, rav *RAV) error {
	if receipt.CollectionID != rav.CollectionID {
		return ErrCollectionMismatch
	}
	if !addressesEqual(receipt.Payer, rav.Payer) {
		return ErrPayerMismatch
	}
	if !addressesEqual(receipt.ServiceProvider, rav.ServiceProvider) {
		return ErrServiceProviderMismatch
	}
	if !addressesEqual(receipt.DataService, rav.DataService) {
		return ErrDataServiceMismatch
	}
	return nil
}

// addressesEqual compares two eth.Address values
func addressesEqual(a, b eth.Address) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

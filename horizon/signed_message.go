package horizon

import (
	"fmt"

	"github.com/streamingfast/eth-go"
)

// SignedMessage wraps a message with its EIP-712 signature
type SignedMessage[T any] struct {
	Message   T             `json:"message"`
	Signature eth.Signature `json:"signature"`
}

// SignedReceipt is a receipt with its signature
type SignedReceipt = SignedMessage[*Receipt]

// SignedRAV is a RAV with its signature
type SignedRAV = SignedMessage[*RAV]

// Sign creates a signed message using the domain and private key
func Sign[T EIP712Encodable](domain *Domain, message T, key *eth.PrivateKey) (*SignedMessage[T], error) {
	messageHash, err := HashTypedData(domain, message)
	if err != nil {
		return nil, fmt.Errorf("computing typed data hash: %w", err)
	}

	sig, err := key.Sign(messageHash)
	if err != nil {
		return nil, fmt.Errorf("signing message: %w", err)
	}

	return &SignedMessage[T]{
		Message:   message,
		Signature: sig,
	}, nil
}

// RecoverSigner recovers the signer address from the signature
func (sm *SignedMessage[T]) RecoverSigner(domain *Domain) (eth.Address, error) {
	// Type assertion to get the EIP712Encodable interface
	msg, ok := any(sm.Message).(EIP712Encodable)
	if !ok {
		return eth.Address{}, fmt.Errorf("message does not implement EIP712Encodable")
	}

	messageHash, err := HashTypedData(domain, msg)
	if err != nil {
		return eth.Address{}, fmt.Errorf("computing typed data hash: %w", err)
	}

	return sm.Signature.Recover(messageHash)
}

// UniqueID returns the signature bytes for uniqueness checking
// Uses normalized (low-S) form to prevent malleability attacks
func (sm *SignedMessage[T]) UniqueID() [65]byte {
	return normalizeSignature(sm.Signature)
}

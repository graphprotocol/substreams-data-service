package integration

import (
	"fmt"
	"math/big"

	"github.com/streamingfast/eth-go"
	"golang.org/x/crypto/sha3"
)

// GenerateSignerProof generates a proof for authorizing a signer.
// The proof is the signer's signature over a message containing:
// - chainId (uint256)
// - collectorAddress (address - the contract address)
// - "authorizeSignerProof" (string literal)
// - proofDeadline (uint256)
// - authorizer (address - msg.sender who will call authorizeSigner)
//
// This matches the Solidity verification in Authorizable.sol:
//
//	bytes32 messageHash = keccak256(
//	    abi.encodePacked(block.chainid, address(this), "authorizeSignerProof", _proofDeadline, msg.sender)
//	);
//	bytes32 digest = MessageHashUtils.toEthSignedMessageHash(messageHash);
//	require(ECDSA.recover(digest, _proof) == _signer, AuthorizableInvalidSignerProof());
func GenerateSignerProof(
	chainID uint64,
	collectorAddress eth.Address,
	proofDeadline uint64,
	authorizer eth.Address,
	signerKey *eth.PrivateKey,
) ([]byte, error) {
	// Build message: abi.encodePacked(chainid, address(this), "authorizeSignerProof", deadline, msg.sender)
	// encodePacked uses tight packing (no padding for addresses)
	message := make([]byte, 0, 124) // 32 + 20 + 20 + 32 + 20 = 124 bytes

	// chainId as uint256 (32 bytes, big-endian)
	chainIDBytes := make([]byte, 32)
	new(big.Int).SetUint64(chainID).FillBytes(chainIDBytes)
	message = append(message, chainIDBytes...)

	// collectorAddress (20 bytes, NOT left-padded - encodePacked)
	message = append(message, collectorAddress[:]...)

	// "authorizeSignerProof" (20 bytes string literal)
	message = append(message, []byte("authorizeSignerProof")...)

	// proofDeadline as uint256 (32 bytes, big-endian)
	deadlineBytes := make([]byte, 32)
	new(big.Int).SetUint64(proofDeadline).FillBytes(deadlineBytes)
	message = append(message, deadlineBytes...)

	// authorizer address (20 bytes, NOT left-padded - encodePacked)
	message = append(message, authorizer[:]...)

	// Hash the message
	messageHash := keccak256(message)

	// Create Ethereum signed message hash: keccak256("\x19Ethereum Signed Message:\n32" + hash)
	prefix := []byte("\x19Ethereum Signed Message:\n32")
	digest := keccak256(append(prefix, messageHash...))

	// Sign with signer's key
	// eth.PrivateKey.Sign expects a 32-byte hash and returns signature in V+R+S format
	sig, err := signerKey.Sign(digest)
	if err != nil {
		return nil, fmt.Errorf("signing proof: %w", err)
	}

	// The eth-go library returns signature as V (1 byte) + R (32 bytes) + S (32 bytes) = 65 bytes
	// Solidity ECDSA.recover expects R (32 bytes) + S (32 bytes) + V (1 byte) format
	proof := make([]byte, 65)
	copy(proof[0:32], sig[1:33])   // R
	copy(proof[32:64], sig[33:65]) // S
	proof[64] = sig[0]             // V

	return proof, nil
}

// keccak256 computes the Keccak-256 hash of the input
func keccak256(data []byte) []byte {
	h := sha3.NewLegacyKeccak256()
	h.Write(data)
	return h.Sum(nil)
}

package main

import (
	"fmt"
	"math/big"
	"time"

	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/streamingfast/eth-go"
)

func main() {
	fmt.Println("Horizon RAV Example")
	fmt.Println("===================\n")

	// Setup EIP-712 domain for Arbitrum One (chain ID 42161)
	chainID := uint64(42161)
	verifyingContract := eth.MustNewAddress("0x1234567890123456789012345678901234567890")
	domain := horizon.NewDomain(chainID, verifyingContract)

	fmt.Printf("Domain: %s v%s (Chain ID: %d)\n", domain.Name, domain.Version, chainID)
	fmt.Printf("Verifying Contract: %s\n\n", verifyingContract.Pretty())

	// Generate keys
	senderKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		panic(err)
	}
	aggregatorKey, err := eth.NewRandomPrivateKey()
	if err != nil {
		panic(err)
	}

	senderAddr := senderKey.PublicKey().Address()
	aggregatorAddr := aggregatorKey.PublicKey().Address()

	fmt.Printf("Sender Address: %s\n", senderAddr.Pretty())
	fmt.Printf("Aggregator Address: %s\n\n", aggregatorAddr.Pretty())

	// Setup collection and addresses
	var collectionID horizon.CollectionID
	copy(collectionID[:], eth.MustNewHash("0xabababababababababababababababababababababababababababababababab")[:])

	payer := senderAddr
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	fmt.Printf("Collection ID: %x\n", collectionID[:8])
	fmt.Printf("Data Service: %s\n", dataService.Pretty())
	fmt.Printf("Service Provider: %s\n\n", serviceProvider.Pretty())

	// Create multiple receipts
	fmt.Println("Creating receipts...")
	var receipts []*horizon.SignedReceipt
	baseTimestamp := uint64(time.Now().UnixNano())
	totalValue := big.NewInt(0)

	for i := 0; i < 5; i++ {
		value := big.NewInt(int64((i + 1) * 100))
		receipt := &horizon.Receipt{
			CollectionID:    collectionID,
			Payer:           payer,
			DataService:     dataService,
			ServiceProvider: serviceProvider,
			TimestampNs:     baseTimestamp + uint64(i*1000000), // Add 1ms between each
			Nonce:           uint64(i + 1),
			Value:           value,
		}

		signedReceipt, err := horizon.Sign(domain, receipt, senderKey)
		if err != nil {
			panic(err)
		}

		// Verify signature
		recoveredSigner, err := signedReceipt.RecoverSigner(domain)
		if err != nil {
			panic(err)
		}

		fmt.Printf("  Receipt #%d: value=%s GRT, signer verified: %v\n",
			i+1, value.String(), addressesEqual(recoveredSigner, senderAddr))

		receipts = append(receipts, signedReceipt)
		totalValue.Add(totalValue, value)
	}

	fmt.Printf("\nTotal receipt value: %s GRT\n\n", totalValue.String())

	// Create aggregator with accepted signers
	aggregator := horizon.NewAggregator(
		domain,
		aggregatorKey,
		[]eth.Address{senderAddr, aggregatorAddr},
	)

	// First aggregation
	fmt.Println("Aggregating receipts (batch 1)...")
	signedRAV1, err := aggregator.AggregateReceipts(receipts[:3], nil)
	if err != nil {
		panic(err)
	}

	rav1Signer, err := signedRAV1.RecoverSigner(domain)
	if err != nil {
		panic(err)
	}

	fmt.Printf("  RAV #1 created:\n")
	fmt.Printf("    Value: %s GRT\n", signedRAV1.Message.ValueAggregate.String())
	fmt.Printf("    Timestamp: %d\n", signedRAV1.Message.TimestampNs)
	fmt.Printf("    Signer: %s (verified: %v)\n\n",
		rav1Signer.Pretty(), addressesEqual(rav1Signer, aggregatorAddr))

	// Incremental aggregation
	fmt.Println("Aggregating remaining receipts (batch 2)...")
	signedRAV2, err := aggregator.AggregateReceipts(receipts[3:], signedRAV1)
	if err != nil {
		panic(err)
	}

	rav2Signer, err := signedRAV2.RecoverSigner(domain)
	if err != nil {
		panic(err)
	}

	fmt.Printf("  RAV #2 created (incremental):\n")
	fmt.Printf("    Value: %s GRT (was %s GRT)\n",
		signedRAV2.Message.ValueAggregate.String(),
		signedRAV1.Message.ValueAggregate.String())
	fmt.Printf("    Timestamp: %d (was %d)\n",
		signedRAV2.Message.TimestampNs,
		signedRAV1.Message.TimestampNs)
	fmt.Printf("    Signer: %s (verified: %v)\n",
		rav2Signer.Pretty(), addressesEqual(rav2Signer, aggregatorAddr))

	// Verify final total
	fmt.Printf("\nFinal aggregated value: %s GRT\n", signedRAV2.Message.ValueAggregate.String())
	fmt.Printf("Expected total: %s GRT\n", totalValue.String())
	fmt.Printf("Values match: %v\n", signedRAV2.Message.ValueAggregate.Cmp(totalValue) == 0)

	fmt.Println("\nExample completed successfully!")
}

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

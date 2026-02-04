package integration

import (
	"context"
	"math/big"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	consumersidecar "github.com/graphprotocol/substreams-data-service/consumer/sidecar"
	"github.com/graphprotocol/substreams-data-service/horizon"
	"github.com/graphprotocol/substreams-data-service/horizon/devenv"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	consumerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/consumer/v1/consumerv1connect"
	providerv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1"
	"github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/provider/v1/providerv1connect"
	providersidecar "github.com/graphprotocol/substreams-data-service/provider/sidecar"
	"github.com/graphprotocol/substreams-data-service/sidecar"
)

// TestPaymentFlowBasic tests a basic payment flow:
// 1. Consumer sidecar Init -> creates session with initial RAV
// 2. Provider sidecar validates the RAV
// 3. Usage reporting and RAV updates
// 4. Session ends with final RAV
func TestPaymentFlowBasic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Get the shared development environment
	env := devenv.Get()
	require.NotNil(t, env, "devenv not started")

	// Setup test with authorized signer
	setup, err := env.SetupTestWithSigner(nil)
	require.NoError(t, err, "failed to setup test")

	// Create domain for signature verification
	domain := env.Domain()

	// Create consumer sidecar
	consumerConfig := &consumersidecar.Config{
		ListenAddr: ":19002",
		SignerKey:  setup.SignerKey,
		Domain:     domain,
	}
	consumerSidecar := consumersidecar.New(consumerConfig, zlog.Named("consumer"))
	go consumerSidecar.Run()
	defer consumerSidecar.Shutdown(nil)
	time.Sleep(100 * time.Millisecond) // Wait for server to start

	// Create provider sidecar
	providerConfig := &providersidecar.Config{
		ListenAddr:      ":19001",
		ServiceProvider: env.ServiceProvider.Address,
		Domain:          domain,
		AcceptedSigners: []eth.Address{setup.SignerAddr},
	}
	providerSidecar := providersidecar.New(providerConfig, zlog.Named("provider"))
	go providerSidecar.Run()
	defer providerSidecar.Shutdown(nil)
	time.Sleep(100 * time.Millisecond) // Wait for server to start

	// Create clients
	consumerClient := consumerv1connect.NewConsumerSidecarServiceClient(
		http.DefaultClient,
		"http://localhost:19002",
	)
	providerClient := providerv1connect.NewProviderSidecarServiceClient(
		http.DefaultClient,
		"http://localhost:19001",
	)

	// Step 1: Consumer Init - creates session with initial RAV
	t.Log("Step 1: Consumer Init")
	initReq := &consumerv1.InitRequest{
		EscrowAccount: &commonv1.EscrowAccount{
			Payer:       commonv1.AddressFromEth(env.Payer.Address),
			Receiver:    commonv1.AddressFromEth(env.ServiceProvider.Address),
			DataService: commonv1.AddressFromEth(env.DataService.Address),
		},
		ProviderEndpoint: "http://localhost:19001",
	}
	initResp, err := consumerClient.Init(ctx, connect.NewRequest(initReq))
	require.NoError(t, err, "consumer Init failed")
	require.NotNil(t, initResp.Msg.PaymentRav, "expected payment RAV")
	require.NotEmpty(t, initResp.Msg.Session.SessionId, "expected session ID")

	consumerSessionID := initResp.Msg.Session.SessionId
	paymentRAV := initResp.Msg.PaymentRav
	t.Logf("Consumer session created: %s", consumerSessionID)

	// Step 2: Provider validates the RAV
	t.Log("Step 2: Provider validates RAV")
	validateReq := &providerv1.ValidatePaymentRequest{
		PaymentRav: paymentRAV,
		ServiceParams: &commonv1.ServiceParameters{
			RequiredBlocksPreproc: 1000,
			PricePerBlock:         commonv1.BigIntFromNative(big.NewInt(1000000)), // 0.001 GRT per block
		},
	}
	validateResp, err := providerClient.ValidatePayment(ctx, connect.NewRequest(validateReq))
	require.NoError(t, err, "provider ValidatePayment failed")
	assert.True(t, validateResp.Msg.Valid, "RAV should be valid: %s", validateResp.Msg.RejectionReason)
	require.NotEmpty(t, validateResp.Msg.SessionId, "expected provider session ID")

	providerSessionID := validateResp.Msg.SessionId
	t.Logf("Provider validated RAV, session: %s", providerSessionID)

	// Step 3: Report usage on consumer side
	t.Log("Step 3: Report usage on consumer side")
	reportReq := &consumerv1.ReportUsageRequest{
		SessionId: consumerSessionID,
		Usage: &commonv1.Usage{
			BlocksProcessed:  100,
			BytesTransferred: 50000,
			Requests:         1,
			Cost:             commonv1.BigIntFromNative(big.NewInt(100000000)), // 0.1 GRT
		},
	}
	reportResp, err := consumerClient.ReportUsage(ctx, connect.NewRequest(reportReq))
	require.NoError(t, err, "consumer ReportUsage failed")
	assert.True(t, reportResp.Msg.ShouldContinue, "session should continue")
	assert.NotNil(t, reportResp.Msg.UpdatedRav, "expected updated RAV")
	t.Log("Consumer reported usage, got updated RAV")

	// Step 4: End session on consumer side
	t.Log("Step 4: End session")
	endReq := &consumerv1.EndSessionRequest{
		SessionId: consumerSessionID,
		FinalUsage: &commonv1.Usage{
			BlocksProcessed:  50,
			BytesTransferred: 25000,
			Requests:         1,
			Cost:             commonv1.BigIntFromNative(big.NewInt(50000000)), // 0.05 GRT
		},
	}
	endResp, err := consumerClient.EndSession(ctx, connect.NewRequest(endReq))
	require.NoError(t, err, "consumer EndSession failed")
	assert.NotNil(t, endResp.Msg.FinalRav, "expected final RAV")
	assert.Equal(t, uint64(150), endResp.Msg.TotalUsage.BlocksProcessed, "expected 150 total blocks")

	// Convert final RAV value
	finalValue := endResp.Msg.FinalRav.Rav.ValueAggregate.ToNative()
	t.Logf("Session ended. Final RAV value: %s", finalValue.String())

	t.Log("Payment flow test completed successfully!")
}

// TestRAVSignatureVerification tests that the provider sidecar correctly
// verifies RAV signatures and rejects invalid ones
func TestRAVSignatureVerification(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()

	// Get the shared development environment
	env := devenv.Get()
	require.NotNil(t, env, "devenv not started")

	// Setup test with authorized signer
	setup, err := env.SetupTestWithSigner(nil)
	require.NoError(t, err, "failed to setup test")

	domain := env.Domain()

	// Create provider sidecar with specific accepted signers
	providerConfig := &providersidecar.Config{
		ListenAddr:      ":19003",
		ServiceProvider: env.ServiceProvider.Address,
		Domain:          domain,
		AcceptedSigners: []eth.Address{setup.SignerAddr},
	}
	providerSidecar := providersidecar.New(providerConfig, zlog.Named("provider"))
	go providerSidecar.Run()
	defer providerSidecar.Shutdown(nil)
	time.Sleep(100 * time.Millisecond)

	providerClient := providerv1connect.NewProviderSidecarServiceClient(
		http.DefaultClient,
		"http://localhost:19003",
	)

	// Create a RAV signed by the authorized signer
	t.Log("Testing valid RAV signature")
	rav := &horizon.RAV{
		Payer:           env.Payer.Address,
		DataService:     env.DataService.Address,
		ServiceProvider: env.ServiceProvider.Address,
		TimestampNs:     uint64(time.Now().UnixNano()),
		ValueAggregate:  big.NewInt(0),
		Metadata:        nil,
	}
	signedRAV, err := horizon.Sign(domain, rav, setup.SignerKey)
	require.NoError(t, err, "failed to sign RAV")

	protoRAV := sidecar.HorizonSignedRAVToProto(signedRAV)

	validateResp, err := providerClient.ValidatePayment(ctx, connect.NewRequest(&providerv1.ValidatePaymentRequest{
		PaymentRav: protoRAV,
	}))
	require.NoError(t, err)
	assert.True(t, validateResp.Msg.Valid, "valid RAV should be accepted: %s", validateResp.Msg.RejectionReason)

	// Create a RAV signed by an unauthorized signer
	t.Log("Testing invalid RAV signature (unauthorized signer)")
	unauthorizedKey, err := eth.NewRandomPrivateKey()
	require.NoError(t, err)

	invalidSignedRAV, err := horizon.Sign(domain, rav, unauthorizedKey)
	require.NoError(t, err, "failed to sign RAV with unauthorized key")

	invalidProtoRAV := sidecar.HorizonSignedRAVToProto(invalidSignedRAV)

	validateResp2, err := providerClient.ValidatePayment(ctx, connect.NewRequest(&providerv1.ValidatePaymentRequest{
		PaymentRav: invalidProtoRAV,
	}))
	require.NoError(t, err)
	assert.False(t, validateResp2.Msg.Valid, "invalid RAV should be rejected")
	assert.Contains(t, validateResp2.Msg.RejectionReason, "not authorized")

	t.Log("Signature verification test completed successfully!")
}

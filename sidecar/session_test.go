package sidecar

import (
	"math/big"
	"testing"

	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
)

func TestNewSession(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := NewSession(payer, receiver, dataService)

	assert.NotEmpty(t, session.ID)
	assert.Equal(t, SessionStateActive, session.State)
	assert.Equal(t, payer, session.Payer)
	assert.Equal(t, receiver, session.Receiver)
	assert.Equal(t, dataService, session.DataService)
	assert.NotNil(t, session.TotalCost)
	assert.Equal(t, int64(0), session.TotalCost.Int64())
}

func TestSession_AddUsage(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := NewSession(payer, receiver, dataService)

	// Add usage
	cost := big.NewInt(1000)
	session.AddUsage(100, 5000, 1, cost)

	assert.Equal(t, uint64(100), session.BlocksProcessed)
	assert.Equal(t, uint64(5000), session.BytesTransferred)
	assert.Equal(t, uint64(1), session.Requests)
	assert.Equal(t, int64(1000), session.TotalCost.Int64())

	// Add more usage
	session.AddUsage(50, 2500, 2, big.NewInt(500))

	assert.Equal(t, uint64(150), session.BlocksProcessed)
	assert.Equal(t, uint64(7500), session.BytesTransferred)
	assert.Equal(t, uint64(3), session.Requests)
	assert.Equal(t, int64(1500), session.TotalCost.Int64())
}

func TestSession_GetUsage(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := NewSession(payer, receiver, dataService)
	session.AddUsage(100, 5000, 3, big.NewInt(1000))

	usage := session.GetUsage()

	assert.Equal(t, uint64(100), usage.BlocksProcessed)
	assert.Equal(t, uint64(5000), usage.BytesTransferred)
	assert.Equal(t, uint64(3), usage.Requests)
	assert.Equal(t, int64(1000), usage.Cost.ToNative().Int64())
}

func TestSession_End(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := NewSession(payer, receiver, dataService)

	assert.True(t, session.IsActive())

	session.End(commonv1.EndReason_END_REASON_COMPLETE)

	assert.False(t, session.IsActive())
	assert.Equal(t, SessionStateEnded, session.State)
	assert.NotNil(t, session.EndedAt)
	assert.Equal(t, commonv1.EndReason_END_REASON_COMPLETE, session.EndReason)
}

func TestSessionManager_Create(t *testing.T) {
	sm := NewSessionManager()

	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := sm.Create(payer, receiver, dataService)

	assert.NotEmpty(t, session.ID)
	assert.Equal(t, 1, sm.Count())
}

func TestSessionManager_Get(t *testing.T) {
	sm := NewSessionManager()

	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := sm.Create(payer, receiver, dataService)

	// Get existing session
	found, err := sm.Get(session.ID)
	require.NoError(t, err)
	assert.Equal(t, session.ID, found.ID)

	// Get non-existent session
	_, err = sm.Get("non-existent")
	assert.Error(t, err)
}

func TestSessionManager_Delete(t *testing.T) {
	sm := NewSessionManager()

	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session := sm.Create(payer, receiver, dataService)
	assert.Equal(t, 1, sm.Count())

	sm.Delete(session.ID)
	assert.Equal(t, 0, sm.Count())

	_, err := sm.Get(session.ID)
	assert.Error(t, err)
}

func TestSessionManager_GetActive(t *testing.T) {
	sm := NewSessionManager()

	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	receiver := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	dataService := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	session1 := sm.Create(payer, receiver, dataService)
	session2 := sm.Create(payer, receiver, dataService)

	// Both sessions are active
	active := sm.GetActive()
	assert.Len(t, active, 2)

	// End one session
	session1.End(commonv1.EndReason_END_REASON_COMPLETE)

	// Only one session should be active now
	active = sm.GetActive()
	assert.Len(t, active, 1)
	assert.Equal(t, session2.ID, active[0].ID)
}

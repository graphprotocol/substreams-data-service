package sidecar

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/graphprotocol/substreams-data-service/horizon"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	"github.com/streamingfast/eth-go"
	"github.com/stretchr/testify/assert"
)

func TestProtoRAVToHorizon(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	protoRAV := &commonv1.RAV{
		Payer:           commonv1.AddressFromEth(payer),
		DataService:     commonv1.AddressFromEth(dataService),
		ServiceProvider: commonv1.AddressFromEth(serviceProvider),
		TimestampNs:     1234567890,
		ValueAggregate:  commonv1.BigIntFromNative(big.NewInt(1000)),
		Metadata:        []byte("test-metadata"),
	}

	result := ProtoRAVToHorizon(protoRAV)

	assert.NotNil(t, result)
	assert.True(t, bytes.Equal(payer, result.Payer))
	assert.True(t, bytes.Equal(dataService, result.DataService))
	assert.True(t, bytes.Equal(serviceProvider, result.ServiceProvider))
	assert.Equal(t, uint64(1234567890), result.TimestampNs)
	assert.Equal(t, int64(1000), result.ValueAggregate.Int64())
}

func TestHorizonRAVToProto(t *testing.T) {
	payer := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	dataService := eth.MustNewAddress("0x2222222222222222222222222222222222222222")
	serviceProvider := eth.MustNewAddress("0x3333333333333333333333333333333333333333")

	horizonRAV := &horizon.RAV{
		Payer:           payer,
		DataService:     dataService,
		ServiceProvider: serviceProvider,
		TimestampNs:     1234567890,
		ValueAggregate:  big.NewInt(1000),
		Metadata:        []byte("test-metadata"),
	}

	result := HorizonRAVToProto(horizonRAV)

	assert.NotNil(t, result)
	assert.True(t, bytes.Equal(payer, result.Payer.ToEth()))
	assert.True(t, bytes.Equal(dataService, result.DataService.ToEth()))
	assert.True(t, bytes.Equal(serviceProvider, result.ServiceProvider.ToEth()))
	assert.Equal(t, uint64(1234567890), result.TimestampNs)
	assert.Equal(t, big.NewInt(1000).Bytes(), result.ValueAggregate.Bytes)
}

func TestAddressesEqual(t *testing.T) {
	addr1 := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	addr2 := eth.MustNewAddress("0x1111111111111111111111111111111111111111")
	addr3 := eth.MustNewAddress("0x2222222222222222222222222222222222222222")

	assert.True(t, AddressesEqual(addr1, addr2))
	assert.False(t, AddressesEqual(addr1, addr3))
}

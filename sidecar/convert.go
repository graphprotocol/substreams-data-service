package sidecar

import (
	"bytes"

	"github.com/graphprotocol/substreams-data-service/horizon"
	commonv1 "github.com/graphprotocol/substreams-data-service/pb/graph/substreams/data_service/common/v1"
	"github.com/streamingfast/eth-go"
)

// ProtoRAVToHorizon converts a proto RAV to a horizon RAV
func ProtoRAVToHorizon(pr *commonv1.RAV) *horizon.RAV {
	if pr == nil {
		return nil
	}

	var collectionID horizon.CollectionID
	if len(pr.Metadata) >= 32 {
		copy(collectionID[:], pr.Metadata[:32])
	}

	return &horizon.RAV{
		CollectionID:    collectionID,
		Payer:           pr.Payer.ToEth(),
		DataService:     pr.DataService.ToEth(),
		ServiceProvider: pr.ServiceProvider.ToEth(),
		TimestampNs:     pr.TimestampNs,
		ValueAggregate:  pr.ValueAggregate.ToNative(),
		Metadata:        pr.Metadata,
	}
}

// HorizonRAVToProto converts a horizon RAV to a proto RAV
func HorizonRAVToProto(hr *horizon.RAV) *commonv1.RAV {
	if hr == nil {
		return nil
	}

	return &commonv1.RAV{
		Payer:           commonv1.AddressFromEth(hr.Payer),
		DataService:     commonv1.AddressFromEth(hr.DataService),
		ServiceProvider: commonv1.AddressFromEth(hr.ServiceProvider),
		TimestampNs:     hr.TimestampNs,
		ValueAggregate:  commonv1.BigIntFromNative(hr.ValueAggregate),
		Metadata:        hr.Metadata,
	}
}

// ProtoSignedRAVToHorizon converts a proto SignedRAV to a horizon SignedRAV
func ProtoSignedRAVToHorizon(psr *commonv1.SignedRAV) *horizon.SignedRAV {
	if psr == nil {
		return nil
	}

	rav := ProtoRAVToHorizon(psr.Rav)
	if rav == nil {
		return nil
	}

	var sig eth.Signature
	copy(sig[:], psr.Signature)

	return &horizon.SignedRAV{
		Message:   rav,
		Signature: sig,
	}
}

// HorizonSignedRAVToProto converts a horizon SignedRAV to a proto SignedRAV
func HorizonSignedRAVToProto(hsr *horizon.SignedRAV) *commonv1.SignedRAV {
	if hsr == nil {
		return nil
	}

	return &commonv1.SignedRAV{
		Rav:       HorizonRAVToProto(hsr.Message),
		Signature: hsr.Signature[:],
	}
}

// AddressesEqual compares two eth.Address values
func AddressesEqual(a, b eth.Address) bool {
	return bytes.Equal(a, b)
}

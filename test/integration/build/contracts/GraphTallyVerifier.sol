// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity ^0.8.20;

import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/**
 * @title GraphTallyVerifier
 * @notice A minimal contract for testing EIP-712 signature verification.
 * This contract implements the same EIP-712 encoding as GraphTallyCollector
 * but without the protocol dependencies (staking, escrow, etc.).
 * Used for integration testing Go signature compatibility.
 */
contract GraphTallyVerifier is EIP712 {
    /// @notice The EIP712 typehash for the ReceiptAggregateVoucher struct
    /// This MUST match GraphTallyCollector's type hash exactly
    bytes32 public constant EIP712_RAV_TYPEHASH =
        keccak256(
            "ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)"
        );

    /// @notice The Receipt Aggregate Voucher (RAV) struct
    struct ReceiptAggregateVoucher {
        bytes32 collectionId;
        address payer;
        address serviceProvider;
        address dataService;
        uint64 timestampNs;
        uint128 valueAggregate;
        bytes metadata;
    }

    /// @notice A struct representing a signed RAV
    struct SignedRAV {
        ReceiptAggregateVoucher rav;
        bytes signature;
    }

    /**
     * @notice Constructs the verifier with the same EIP-712 domain as GraphTallyCollector
     * @dev Domain name is "GraphTallyCollector" and version is "1"
     */
    constructor() EIP712("GraphTallyCollector", "1") {}

    /**
     * @notice Returns the EIP-712 domain separator
     * @return The domain separator bytes32 hash
     */
    function domainSeparator() external view returns (bytes32) {
        return _domainSeparatorV4();
    }

    /**
     * @notice Recovers the signer address of a signed ReceiptAggregateVoucher (RAV)
     * @param signedRAV The SignedRAV containing the RAV and its signature
     * @return The address of the signer
     */
    function recoverRAVSigner(SignedRAV calldata signedRAV) external view returns (address) {
        bytes32 messageHash = _encodeRAV(signedRAV.rav);
        return ECDSA.recover(messageHash, signedRAV.signature);
    }

    /**
     * @notice Computes the EIP-712 hash of a ReceiptAggregateVoucher (RAV)
     * @param rav The RAV for which to compute the hash
     * @return The EIP-712 typed data hash
     */
    function encodeRAV(ReceiptAggregateVoucher calldata rav) external view returns (bytes32) {
        return _encodeRAV(rav);
    }

    /**
     * @notice Computes the struct hash of a RAV (without domain prefix)
     * @param rav The RAV for which to compute the struct hash
     * @return The keccak256 hash of the struct encoding
     */
    function structHash(ReceiptAggregateVoucher calldata rav) external pure returns (bytes32) {
        return keccak256(
            abi.encode(
                EIP712_RAV_TYPEHASH,
                rav.collectionId,
                rav.payer,
                rav.serviceProvider,
                rav.dataService,
                rav.timestampNs,
                rav.valueAggregate,
                keccak256(rav.metadata)
            )
        );
    }

    /**
     * @notice Internal function to compute the EIP-712 hash
     */
    function _encodeRAV(ReceiptAggregateVoucher memory _rav) private view returns (bytes32) {
        return _hashTypedDataV4(
            keccak256(
                abi.encode(
                    EIP712_RAV_TYPEHASH,
                    _rav.collectionId,
                    _rav.payer,
                    _rav.serviceProvider,
                    _rav.dataService,
                    _rav.timestampNs,
                    _rav.valueAggregate,
                    keccak256(_rav.metadata)
                )
            )
        );
    }
}

// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

/**
 * @title Original Contracts Wrapper
 * @notice This file imports ORIGINAL contracts from horizon-contracts submodule
 *         so they get compiled alongside our test mocks.
 *
 * These are the REAL Graph Protocol contracts that we test against:
 *   - PaymentsEscrow: 3-level escrow mapping (payer->collector->receiver)
 *   - GraphPayments: Payment distribution (protocol, data service, delegation)
 *   - GraphTallyCollector: RAV verification with EIP-712 signing and Authorizable
 *
 * By testing against original contracts, we ensure our Go implementation
 * is compatible with the actual production contracts.
 */

// Import original contracts from horizon-contracts submodule
// These will be compiled and their artifacts extracted for use in tests
import {PaymentsEscrow} from "@graphprotocol/horizon/contracts/payments/PaymentsEscrow.sol";
import {GraphPayments} from "@graphprotocol/horizon/contracts/payments/GraphPayments.sol";
import {GraphTallyCollector} from "@graphprotocol/horizon/contracts/payments/collectors/GraphTallyCollector.sol";

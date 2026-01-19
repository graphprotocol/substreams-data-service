// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

import { IGraphPayments } from "@graphprotocol/interfaces/contracts/horizon/IGraphPayments.sol";
import { IGraphTallyCollector } from "@graphprotocol/interfaces/contracts/horizon/IGraphTallyCollector.sol";
import { IDataService } from "@graphprotocol/interfaces/contracts/data-service/IDataService.sol";

import { Initializable } from "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import { OwnableUpgradeable } from "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import { DataService } from "@graphprotocol/horizon/contracts/data-service/DataService.sol";

/**
 * @title SubstreamsDataService
 * @notice A minimal data service contract for Substreams indexing and querying
 * @dev Implements the strict minimum required for a DataService to work with GraphTallyCollector
 */
contract SubstreamsDataService is
    Initializable,
    OwnableUpgradeable,
    DataService,
    IDataService
{
    /// @notice GraphTallyCollector address for payment collection
    IGraphTallyCollector public immutable GRAPH_TALLY_COLLECTOR;

    /// @notice Mapping of service provider to their registration status
    mapping(address => bool) public isRegistered;

    /// @notice Mapping of service provider to their payments destination
    mapping(address => address) public paymentsDestination;

    /// @notice Emitted when an indexer sets their payments destination
    event PaymentsDestinationSet(address indexed indexer, address indexed destination);

    /// @notice Error when indexer is not registered
    error SubstreamsDataServiceIndexerNotRegistered(address indexer);

    /// @notice Error when service provider in RAV doesn't match caller
    error SubstreamsDataServiceIndexerMismatch(address ravServiceProvider, address indexer);

    /// @notice Error when payment type is not supported
    error SubstreamsDataServiceInvalidPaymentType(IGraphPayments.PaymentTypes paymentType);

    /**
     * @notice Modifier to check if indexer is registered
     */
    modifier onlyRegisteredIndexer(address indexer) {
        require(isRegistered[indexer], SubstreamsDataServiceIndexerNotRegistered(indexer));
        _;
    }

    /**
     * @notice Constructor
     * @param controller The Graph Controller address
     * @param graphTallyCollector The GraphTallyCollector address
     */
    constructor(
        address controller,
        address graphTallyCollector
    ) DataService(controller) {
        GRAPH_TALLY_COLLECTOR = IGraphTallyCollector(graphTallyCollector);
        _disableInitializers();
    }

    /**
     * @notice Initialize the contract
     * @param owner The owner address
     * @param minimumProvisionTokens Minimum tokens required for provision
     */
    function initialize(
        address owner,
        uint256 minimumProvisionTokens
    ) external initializer {
        __Ownable_init(owner);
        __DataService_init();
        _setProvisionTokensRange(minimumProvisionTokens, type(uint256).max);
    }

    /// @inheritdoc IDataService
    function register(
        address indexer,
        bytes calldata data
    ) external override onlyAuthorizedForProvision(indexer) onlyValidProvision(indexer) {
        (address paymentsDestination_) = abi.decode(data, (address));

        isRegistered[indexer] = true;
        _setPaymentsDestination(indexer, paymentsDestination_);

        emit ServiceProviderRegistered(indexer, data);
    }

    /// @inheritdoc IDataService
    function acceptProvisionPendingParameters(
        address indexer,
        bytes calldata
    ) external override onlyAuthorizedForProvision(indexer) {
        _acceptProvisionParameters(indexer);
        emit ProvisionPendingParametersAccepted(indexer);
    }

    /// @inheritdoc IDataService
    function startService(address, bytes calldata) external pure override {
        // No-op for Substreams - service starts implicitly on registration
    }

    /// @inheritdoc IDataService
    function stopService(address, bytes calldata) external pure override {
        // No-op for Substreams - service stops implicitly on deregistration
    }

    /// @inheritdoc IDataService
    function collect(
        address indexer,
        IGraphPayments.PaymentTypes paymentType,
        bytes calldata data
    ) external override
      onlyAuthorizedForProvision(indexer)
      onlyValidProvision(indexer)
      onlyRegisteredIndexer(indexer)
      returns (uint256)
    {
        uint256 paymentCollected = 0;

        if (paymentType == IGraphPayments.PaymentTypes.QueryFee) {
            paymentCollected = _collectQueryFees(indexer, data);
        } else {
            revert SubstreamsDataServiceInvalidPaymentType(paymentType);
        }

        emit ServicePaymentCollected(indexer, paymentType, paymentCollected);
        return paymentCollected;
    }

    /// @inheritdoc IDataService
    function slash(address, bytes calldata) external pure override {
        // Slashing not implemented in minimal version
        // Would require DisputeManager integration
    }

    /**
     * @notice Set the payments destination for an indexer
     * @param paymentsDestination_ The destination address for payments
     */
    function setPaymentsDestination(address paymentsDestination_) external {
        _setPaymentsDestination(msg.sender, paymentsDestination_);
    }

    /**
     * @notice Collect query fees using GraphTallyCollector
     * @param indexer The indexer address
     * @param data Encoded SignedRAV
     * @return The amount of tokens collected
     */
    function _collectQueryFees(address indexer, bytes calldata data) private returns (uint256) {
        (IGraphTallyCollector.SignedRAV memory signedRav, uint256 dataServiceCut) = abi.decode(
            data,
            (IGraphTallyCollector.SignedRAV, uint256)
        );

        require(
            signedRav.rav.serviceProvider == indexer,
            SubstreamsDataServiceIndexerMismatch(signedRav.rav.serviceProvider, indexer)
        );

        // Collect via GraphTallyCollector
        uint256 tokensCollected = GRAPH_TALLY_COLLECTOR.collect(
            IGraphPayments.PaymentTypes.QueryFee,
            abi.encode(signedRav, dataServiceCut, paymentsDestination[indexer]),
            0 // collect full amount
        );

        return tokensCollected;
    }

    /**
     * @notice Internal function to set payments destination
     */
    function _setPaymentsDestination(address indexer, address destination) internal {
        paymentsDestination[indexer] = destination;
        emit PaymentsDestinationSet(indexer, destination);
    }
}

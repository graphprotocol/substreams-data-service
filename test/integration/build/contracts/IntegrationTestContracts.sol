// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {IHorizonStaking} from "@graphprotocol/interfaces/contracts/horizon/IHorizonStaking.sol";
import {IGraphPayments} from "@graphprotocol/interfaces/contracts/horizon/IGraphPayments.sol";

/**
 * @title Mock GRT Token for testing
 */
contract MockGRTToken is ERC20 {
    constructor() ERC20("Graph Token", "GRT") {
        // Mint 1 billion tokens to deployer
        _mint(msg.sender, 1_000_000_000 * 10**18);
    }

    function mint(address to, uint256 amount) external {
        _mint(to, amount);
    }

    function burn(uint256 amount) external {
        _burn(msg.sender, amount);
    }

    function burnFrom(address from, uint256 amount) external {
        _burn(from, amount);
    }
}

/**
 * @title Mock Controller for testing
 */
contract MockController {
    mapping(bytes32 => address) private _registry;
    address public governor;

    constructor(address governor_) {
        governor = governor_;
    }

    function setContractProxy(bytes32 id, address contractAddress) external {
        _registry[id] = contractAddress;
    }

    function getContractProxy(bytes32 id) external view returns (address) {
        return _registry[id];
    }

    function paused() external pure returns (bool) {
        return false;
    }

    function partialPaused() external pure returns (bool) {
        return false;
    }
}

/**
 * @title Mock Staking contract for testing
 * @dev Implements IHorizonStaking methods required by ProvisionManager and GraphPayments
 */
contract MockStaking {
    // Storage for provisions: serviceProvider => dataService => Provision
    struct ProvisionData {
        uint256 tokens;
        uint256 tokensThawing;
        uint256 sharesThawing;
        uint32 maxVerifierCut;
        uint64 thawingPeriod;
        uint64 createdAt;
        uint32 maxVerifierCutPending;
        uint64 thawingPeriodPending;
        uint256 lastParametersStagedAt;
        uint256 thawingNonce;
    }

    mapping(address => mapping(address => ProvisionData)) private _provisions;
    mapping(address => mapping(address => IHorizonStaking.DelegationPool)) private _delegationPools;
    mapping(address => mapping(address => mapping(IGraphPayments.PaymentTypes => uint256))) private _delegationFeeCuts;

    // Authorized operators: serviceProvider => dataService => operator => authorized
    mapping(address => mapping(address => mapping(address => bool))) private _operators;

    /**
     * @notice Set up a provision for testing
     * @dev This is a test helper - creates a provision with createdAt set
     */
    function setProvision(
        address serviceProvider,
        address dataService,
        uint256 tokens,
        uint32 maxVerifierCut,
        uint64 thawingPeriod
    ) external {
        _provisions[serviceProvider][dataService] = ProvisionData({
            tokens: tokens,
            tokensThawing: 0,
            sharesThawing: 0,
            maxVerifierCut: maxVerifierCut,
            thawingPeriod: thawingPeriod,
            createdAt: uint64(block.timestamp), // Mark as created
            maxVerifierCutPending: maxVerifierCut,
            thawingPeriodPending: thawingPeriod,
            lastParametersStagedAt: 0,
            thawingNonce: 0
        });
    }

    /**
     * @notice Authorize an operator for a service provider
     * @dev Required by ProvisionManager.onlyAuthorizedForProvision
     */
    function setOperator(address serviceProvider, address dataService, address operator, bool authorized) external {
        _operators[serviceProvider][dataService][operator] = authorized;
    }

    // --- IHorizonStaking interface methods ---

    /**
     * @notice Check if caller is authorized for provision
     * @dev Called by ProvisionManager.onlyAuthorizedForProvision modifier
     */
    function isAuthorized(address serviceProvider, address dataService, address caller)
        external view returns (bool) {
        // Service provider is always authorized for themselves
        if (caller == serviceProvider) return true;
        // Check explicit operator authorization
        return _operators[serviceProvider][dataService][caller];
    }

    /**
     * @notice Get provision for service provider
     * @dev Called by ProvisionManager._getProvision
     */
    function getProvision(address serviceProvider, address dataService)
        external view returns (IHorizonStaking.Provision memory provision) {
        ProvisionData storage p = _provisions[serviceProvider][dataService];
        provision.tokens = p.tokens;
        provision.tokensThawing = p.tokensThawing;
        provision.sharesThawing = p.sharesThawing;
        provision.maxVerifierCut = p.maxVerifierCut;
        provision.thawingPeriod = p.thawingPeriod;
        provision.createdAt = p.createdAt;
        provision.maxVerifierCutPending = p.maxVerifierCutPending;
        provision.thawingPeriodPending = p.thawingPeriodPending;
        provision.lastParametersStagedAt = p.lastParametersStagedAt;
        provision.thawingNonce = p.thawingNonce;
        return provision;
    }

    /**
     * @notice Accept provision parameters
     * @dev Called by ProvisionManager._acceptProvisionParameters
     */
    function acceptProvisionParameters(address serviceProvider) external {
        ProvisionData storage p = _provisions[serviceProvider][msg.sender];
        p.maxVerifierCut = p.maxVerifierCutPending;
        p.thawingPeriod = p.thawingPeriodPending;
    }

    /**
     * @notice Get available tokens (not thawing)
     * @dev Used by GraphTallyCollector to check provider has sufficient stake
     */
    function getProviderTokensAvailable(address serviceProvider, address verifier)
        external view returns (uint256) {
        ProvisionData storage p = _provisions[serviceProvider][verifier];
        return p.tokens - p.tokensThawing;
    }

    // --- Methods required by GraphPayments ---

    function getDelegationPool(address serviceProvider, address dataService)
        external view returns (IHorizonStaking.DelegationPool memory) {
        return _delegationPools[serviceProvider][dataService];
    }

    function getDelegationFeeCut(address serviceProvider, address dataService, IGraphPayments.PaymentTypes paymentType)
        external view returns (uint256) {
        return _delegationFeeCuts[serviceProvider][dataService][paymentType];
    }

    function addToDelegationPool(address serviceProvider, address dataService, uint256 tokens) external {
        _delegationPools[serviceProvider][dataService].tokens += tokens;
    }

    function stakeTo(address serviceProvider, uint256 tokens) external {
        _provisions[serviceProvider][msg.sender].tokens += tokens;
    }

    // Test helpers
    function setDelegationFeeCut(
        address serviceProvider,
        address dataService,
        IGraphPayments.PaymentTypes paymentType,
        uint256 cut
    ) external {
        _delegationFeeCuts[serviceProvider][dataService][paymentType] = cut;
    }
}

/**
 * @title Mock PaymentsEscrow for testing
 * @dev Uses 3-level mapping (payer -> collector -> receiver) like the original contract
 */
contract MockPaymentsEscrow {
    IERC20 public immutable graphToken;

    // 3-level mapping: payer => collector => receiver => balance
    mapping(address => mapping(address => mapping(address => uint256))) private _escrowBalances;

    event Deposited(address indexed sender, address indexed collector, address indexed receiver, uint256 amount);
    event Collected(
        address indexed payer,
        address indexed receiver,
        uint256 amount,
        address indexed dataService,
        uint256 dataServiceCut,
        address receiverDestination
    );

    constructor(address _graphToken) {
        graphToken = IERC20(_graphToken);
    }

    /**
     * @notice Deposit tokens to escrow for a specific collector and receiver
     * @param collector The address of the collector (typically GraphTallyCollector)
     * @param receiver The address of the receiver (service provider)
     * @param amount The amount of tokens to deposit
     */
    function deposit(address collector, address receiver, uint256 amount) external {
        require(graphToken.transferFrom(msg.sender, address(this), amount), "Transfer failed");
        _escrowBalances[msg.sender][collector][receiver] += amount;
        emit Deposited(msg.sender, collector, receiver, amount);
    }

    /**
     * @notice Get escrow balance for payer -> collector -> receiver
     */
    function getEscrowAmount(address payer, address collector, address receiver) external view returns (uint256) {
        return _escrowBalances[payer][collector][receiver];
    }

    /**
     * @notice Collect tokens from escrow and approve GraphPayments
     * @dev Called by GraphTallyCollector (msg.sender is the collector)
     */
    function collect(
        uint8 /* paymentType */,
        address payer,
        address receiver,
        uint256 amount,
        address dataService,
        uint256 dataServiceCut,
        address receiverDestination
    ) external {
        require(_escrowBalances[payer][msg.sender][receiver] >= amount, "Insufficient escrow balance");
        _escrowBalances[payer][msg.sender][receiver] -= amount;

        // Calculate cuts
        uint256 dataServiceAmount = (amount * dataServiceCut) / 1_000_000; // PPM
        uint256 receiverAmount = amount - dataServiceAmount;

        // Transfer tokens
        if (dataServiceAmount > 0) {
            require(graphToken.transfer(dataService, dataServiceAmount), "Data service transfer failed");
        }
        if (receiverAmount > 0) {
            address destination = receiverDestination == address(0) ? receiver : receiverDestination;
            require(graphToken.transfer(destination, receiverAmount), "Receiver transfer failed");
        }

        emit Collected(payer, receiver, amount, dataService, dataServiceCut, receiverDestination);
    }
}

/**
 * @title Mock GraphPayments for testing
 */
contract MockGraphPayments {
    function collect(
        uint8 paymentType,
        bytes calldata data
    ) external returns (uint256) {
        // Forward to collector - not implemented in this mock
        return 0;
    }
}

/**
 * @title MockEpochManager
 * @notice Minimal mock for testing - just needs to exist for GraphDirectory
 */
contract MockEpochManager {
    function currentEpoch() external pure returns (uint256) {
        return 1;
    }
}

// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

/**
 * @title Test Mock Contracts
 * @notice These are MOCK contracts used only for testing. They provide minimal implementations
 *         of dependencies that are NOT the focus of our tests.
 *
 * For integration tests, we use ORIGINAL contracts from horizon-contracts for:
 *   - GraphTallyCollector (RAV verification, EIP-712)
 *   - PaymentsEscrow (3-level escrow mapping)
 *   - GraphPayments (payment distribution)
 *
 * These mocks provide the surrounding infrastructure:
 *   - MockGRTToken: Simple ERC20 with IGraphToken interface
 *   - MockController: Contract registry
 *   - MockStaking: IHorizonStaking for provision tracking
 *   - MockEpochManager, MockRewardsManager, MockTokenGateway,
 *     MockProxyAdmin, MockCuration: GraphDirectory stubs
 */

import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import {IHorizonStaking} from "@graphprotocol/interfaces/contracts/horizon/IHorizonStaking.sol";
import {IGraphPayments} from "@graphprotocol/interfaces/contracts/horizon/IGraphPayments.sol";

// ============================================================================
// REQUIRED MOCKS - These implement real functionality for test infrastructure
// ============================================================================

/**
 * @title MockGRTToken
 * @notice Simple ERC20 implementing IGraphToken interface for testing
 * @dev Used by original PaymentsEscrow and GraphPayments contracts
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

    // IGraphToken interface stubs (not needed for our tests but required by interface)
    function addMinter(address) external {}
    function removeMinter(address) external {}
    function renounceMinter() external {}
    function isMinter(address) external pure returns (bool) { return true; }
    function permit(address, address, uint256, uint256, uint8, bytes32, bytes32) external {}
    function increaseAllowance(address spender, uint256 addedValue) external returns (bool) {
        _approve(msg.sender, spender, allowance(msg.sender, spender) + addedValue);
        return true;
    }
    function decreaseAllowance(address spender, uint256 subtractedValue) external returns (bool) {
        uint256 currentAllowance = allowance(msg.sender, spender);
        require(currentAllowance >= subtractedValue, "ERC20: decreased allowance below zero");
        _approve(msg.sender, spender, currentAllowance - subtractedValue);
        return true;
    }
}

/**
 * @title MockController
 * @notice Contract registry for GraphDirectory lookups
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
 * @title MockStaking
 * @notice Implements IHorizonStaking methods required by ProvisionManager and GraphPayments
 */
contract MockStaking {
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
    mapping(address => mapping(address => mapping(address => bool))) private _operators;

    // GRT token for staking operations
    IERC20 public graphToken;

    function setGraphToken(address token) external {
        graphToken = IERC20(token);
    }

    /**
     * @notice Set up a provision for testing
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
            createdAt: uint64(block.timestamp),
            maxVerifierCutPending: maxVerifierCut,
            thawingPeriodPending: thawingPeriod,
            lastParametersStagedAt: 0,
            thawingNonce: 0
        });
    }

    /**
     * @notice Authorize an operator for a service provider
     */
    function setOperator(address serviceProvider, address dataService, address operator, bool authorized) external {
        _operators[serviceProvider][dataService][operator] = authorized;
    }

    // --- IHorizonStaking interface methods ---

    function isAuthorized(address serviceProvider, address dataService, address caller)
        external view returns (bool) {
        if (caller == serviceProvider) return true;
        return _operators[serviceProvider][dataService][caller];
    }

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

    function acceptProvisionParameters(address serviceProvider) external {
        ProvisionData storage p = _provisions[serviceProvider][msg.sender];
        p.maxVerifierCut = p.maxVerifierCutPending;
        p.thawingPeriod = p.thawingPeriodPending;
    }

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
        // Transfer tokens from sender (GraphPayments) to this contract
        if (address(graphToken) != address(0) && tokens > 0) {
            graphToken.transferFrom(msg.sender, address(this), tokens);
        }
    }

    function stakeTo(address serviceProvider, uint256 tokens) external {
        _provisions[serviceProvider][msg.sender].tokens += tokens;
        // Transfer tokens from sender (GraphPayments) to this contract
        if (address(graphToken) != address(0) && tokens > 0) {
            graphToken.transferFrom(msg.sender, address(this), tokens);
        }
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

// ============================================================================
// GRAPHDIRECTORY STUBS - Minimal implementations to satisfy GraphDirectory
// These contracts are NOT tested - they just need to exist in the registry
// ============================================================================

/**
 * @title MockEpochManager
 * @notice Minimal stub for GraphDirectory
 */
contract MockEpochManager {
    function currentEpoch() external pure returns (uint256) {
        return 1;
    }

    function epochLength() external pure returns (uint256) {
        return 6646;
    }
}

/**
 * @title MockRewardsManager
 * @notice Minimal stub for GraphDirectory
 */
contract MockRewardsManager {
    function onSubgraphAllocationUpdate(bytes32) external pure returns (uint256) {
        return 0;
    }

    function takeRewards(address) external pure returns (uint256) {
        return 0;
    }
}

/**
 * @title MockTokenGateway
 * @notice Minimal stub for GraphDirectory
 */
contract MockTokenGateway {
    function outboundTransfer(
        address,
        address,
        uint256,
        uint256,
        uint256,
        bytes calldata
    ) external pure returns (bytes memory) {
        return "";
    }
}

/**
 * @title MockProxyAdmin
 * @notice Minimal stub for GraphDirectory
 */
contract MockProxyAdmin {
    function getProxyImplementation(address) external pure returns (address) {
        return address(0);
    }

    function getProxyAdmin(address) external pure returns (address) {
        return address(0);
    }
}

/**
 * @title MockCuration
 * @notice Minimal stub for GraphDirectory
 */
contract MockCuration {
    function isCurated(bytes32) external pure returns (bool) {
        return false;
    }

    function getCurationPoolSignal(bytes32) external pure returns (uint256) {
        return 0;
    }
}

// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";
import {IERC20} from "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import {ERC20} from "@openzeppelin/contracts/token/ERC20/ERC20.sol";

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
 */
contract MockStaking {
    mapping(address serviceProvider => mapping(address dataService => uint256)) private _provisions;

    function setProvision(address serviceProvider, address dataService, uint256 tokens) external {
        _provisions[serviceProvider][dataService] = tokens;
    }

    function getProviderTokensAvailable(address serviceProvider, address dataService) external view returns (uint256) {
        return _provisions[serviceProvider][dataService];
    }
}

/**
 * @title Mock PaymentsEscrow for testing
 */
contract MockPaymentsEscrow {
    IERC20 public immutable graphToken;

    mapping(address sender => mapping(address receiver => uint256)) private _escrowBalances;

    event Deposited(address indexed sender, address indexed receiver, uint256 amount);
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

    function deposit(address sender, uint256 amount) external {
        require(graphToken.transferFrom(msg.sender, address(this), amount), "Transfer failed");
        _escrowBalances[sender][sender] += amount;
        emit Deposited(sender, sender, amount);
    }

    function getEscrowAmount(address sender, address receiver) external view returns (uint256) {
        return _escrowBalances[sender][receiver];
    }

    function collect(
        uint8 /* paymentType */,
        address payer,
        address receiver,
        uint256 amount,
        address dataService,
        uint256 dataServiceCut,
        address receiverDestination
    ) external {
        require(_escrowBalances[payer][payer] >= amount, "Insufficient escrow balance");
        _escrowBalances[payer][payer] -= amount;

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

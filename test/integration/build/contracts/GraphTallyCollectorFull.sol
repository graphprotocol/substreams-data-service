// SPDX-License-Identifier: GPL-3.0-or-later
pragma solidity 0.8.27;

// This file imports the full GraphTallyCollector contract from the graphprotocol-contracts monorepo
// We need to set up the correct remappings in foundry to make this work

// For now, we'll use a simplified test version that includes the core functionality
// but with mock dependencies to avoid the full protocol stack

import {EIP712} from "@openzeppelin/contracts/utils/cryptography/EIP712.sol";
import {ECDSA} from "@openzeppelin/contracts/utils/cryptography/ECDSA.sol";

/**
 * @title Authorizable - simplified for testing
 */
contract Authorizable {
    mapping(address authorizer => mapping(address authorized => bool)) private _authorized;
    mapping(address authorizer => mapping(address thawing => uint256)) private _thawEndTimestamp;

    uint256 public immutable revokeSignerThawingPeriod;

    event SignerAuthorized(address indexed authorizer, address indexed authorized);
    event SignerAuthorizationRevoked(address indexed authorizer, address indexed authorized);
    event SignerThawing(address indexed authorizer, address indexed thawing, uint256 thawingUntil);

    error AuthorizableSignerAlreadyAuthorized(address signer);
    error AuthorizableSignerNotAuthorized(address signer);
    error AuthorizableAlreadyThawing(address signer);
    error AuthorizableNotThawing(address signer);
    error AuthorizableStillThawing(address signer, uint256 thawingUntil);

    constructor(uint256 _revokeSignerThawingPeriod) {
        revokeSignerThawingPeriod = _revokeSignerThawingPeriod;
    }

    function authorizeSigner(address signer) external {
        require(!_authorized[msg.sender][signer], AuthorizableSignerAlreadyAuthorized(signer));
        _authorized[msg.sender][signer] = true;
        delete _thawEndTimestamp[msg.sender][signer];
        emit SignerAuthorized(msg.sender, signer);
    }

    function revokeSigner(address signer) external {
        require(_authorized[msg.sender][signer], AuthorizableSignerNotAuthorized(signer));
        require(_thawEndTimestamp[msg.sender][signer] <= block.timestamp, AuthorizableStillThawing(signer, _thawEndTimestamp[msg.sender][signer]));
        delete _authorized[msg.sender][signer];
        delete _thawEndTimestamp[msg.sender][signer];
        emit SignerAuthorizationRevoked(msg.sender, signer);
    }

    function thawSigner(address signer) external {
        require(_authorized[msg.sender][signer], AuthorizableSignerNotAuthorized(signer));
        require(_thawEndTimestamp[msg.sender][signer] == 0, AuthorizableAlreadyThawing(signer));
        uint256 thawingUntil = block.timestamp + revokeSignerThawingPeriod;
        _thawEndTimestamp[msg.sender][signer] = thawingUntil;
        emit SignerThawing(msg.sender, signer, thawingUntil);
    }

    function cancelThaw(address signer) external {
        require(_thawEndTimestamp[msg.sender][signer] > 0, AuthorizableNotThawing(signer));
        delete _thawEndTimestamp[msg.sender][signer];
    }

    function isAuthorized(address authorizer, address signer) external view returns (bool) {
        return _isAuthorized(authorizer, signer);
    }

    function _isAuthorized(address authorizer, address signer) internal view returns (bool) {
        return authorizer == signer || _authorized[authorizer][signer];
    }
}

/**
 * @title GraphDirectoryMock - simplified mock for testing
 */
abstract contract GraphDirectoryMock {
    address private immutable GRAPH_TOKEN;
    address private immutable GRAPH_STAKING;
    address private immutable GRAPH_PAYMENTS_ESCROW;

    error GraphDirectoryInvalidZeroAddress(bytes contractName);

    constructor(address controller) {
        MockController c = MockController(controller);

        GRAPH_TOKEN = c.getContractProxy(keccak256("GraphToken"));
        GRAPH_STAKING = c.getContractProxy(keccak256("HorizonStaking"));
        GRAPH_PAYMENTS_ESCROW = c.getContractProxy(keccak256("PaymentsEscrow"));

        require(GRAPH_TOKEN != address(0), GraphDirectoryInvalidZeroAddress("GraphToken"));
        require(GRAPH_STAKING != address(0), GraphDirectoryInvalidZeroAddress("HorizonStaking"));
        require(GRAPH_PAYMENTS_ESCROW != address(0), GraphDirectoryInvalidZeroAddress("PaymentsEscrow"));
    }

    function _graphStaking() internal view returns (MockStaking) {
        return MockStaking(GRAPH_STAKING);
    }

    function _graphPaymentsEscrow() internal view returns (MockPaymentsEscrow) {
        return MockPaymentsEscrow(GRAPH_PAYMENTS_ESCROW);
    }
}

interface MockController {
    function getContractProxy(bytes32 id) external view returns (address);
}

interface MockStaking {
    function getProviderTokensAvailable(address serviceProvider, address dataService) external view returns (uint256);
}

interface MockPaymentsEscrow {
    function collect(
        uint8 paymentType,
        address payer,
        address receiver,
        uint256 amount,
        address dataService,
        uint256 dataServiceCut,
        address receiverDestination
    ) external;
}

/**
 * @title GraphTallyCollectorFull
 * @notice Full implementation of GraphTallyCollector for integration testing
 */
contract GraphTallyCollectorFull is EIP712, GraphDirectoryMock, Authorizable {
    bytes32 private constant EIP712_RAV_TYPEHASH =
        keccak256(
            "ReceiptAggregateVoucher(bytes32 collectionId,address payer,address serviceProvider,address dataService,uint64 timestampNs,uint128 valueAggregate,bytes metadata)"
        );

    struct ReceiptAggregateVoucher {
        bytes32 collectionId;
        address payer;
        address serviceProvider;
        address dataService;
        uint64 timestampNs;
        uint128 valueAggregate;
        bytes metadata;
    }

    struct SignedRAV {
        ReceiptAggregateVoucher rav;
        bytes signature;
    }

    mapping(address dataService => mapping(bytes32 collectionId => mapping(address receiver => mapping(address payer => uint256 tokens))))
        public tokensCollected;

    event PaymentCollected(
        uint8 indexed paymentType,
        bytes32 indexed collectionId,
        address indexed payer,
        address receiver,
        address dataService,
        uint256 tokens
    );

    event RAVCollected(
        bytes32 indexed collectionId,
        address indexed payer,
        address indexed receiver,
        address dataService,
        uint64 timestampNs,
        uint128 valueAggregate,
        bytes metadata,
        bytes signature
    );

    error GraphTallyCollectorCallerNotDataService(address caller, address dataService);
    error GraphTallyCollectorInvalidRAVSigner();
    error GraphTallyCollectorUnauthorizedDataService(address dataService);
    error GraphTallyCollectorInconsistentRAVTokens(uint256 ravTokens, uint256 alreadyCollected);
    error GraphTallyCollectorInvalidTokensToCollectAmount(uint256 tokensToCollect, uint256 availableTokens);

    constructor(
        string memory eip712Name,
        string memory eip712Version,
        address controller,
        uint256 revokeSignerThawingPeriod
    ) EIP712(eip712Name, eip712Version) GraphDirectoryMock(controller) Authorizable(revokeSignerThawingPeriod) {}

    function domainSeparator() external view returns (bytes32) {
        return _domainSeparatorV4();
    }

    function recoverRAVSigner(SignedRAV calldata signedRAV) external view returns (address) {
        return _recoverRAVSigner(signedRAV);
    }

    function encodeRAV(ReceiptAggregateVoucher calldata rav) external view returns (bytes32) {
        return _encodeRAV(rav);
    }

    function collect(uint8 _paymentType, bytes calldata _data) external returns (uint256) {
        return _collect(_paymentType, _data, 0);
    }

    function _collect(
        uint8 _paymentType,
        bytes calldata _data,
        uint256 _tokensToCollect
    ) private returns (uint256) {
        (SignedRAV memory signedRAV, uint256 dataServiceCut, address receiverDestination) = abi.decode(
            _data,
            (SignedRAV, uint256, address)
        );

        require(
            signedRAV.rav.dataService == msg.sender,
            GraphTallyCollectorCallerNotDataService(msg.sender, signedRAV.rav.dataService)
        );

        _requireAuthorizedSigner(signedRAV);

        bytes32 collectionId = signedRAV.rav.collectionId;
        address dataService = signedRAV.rav.dataService;
        address receiver = signedRAV.rav.serviceProvider;

        {
            uint256 tokensAvailable = _graphStaking().getProviderTokensAvailable(
                signedRAV.rav.serviceProvider,
                signedRAV.rav.dataService
            );
            require(tokensAvailable > 0, GraphTallyCollectorUnauthorizedDataService(signedRAV.rav.dataService));
        }

        uint256 tokensToCollect = 0;
        {
            uint256 tokensRAV = signedRAV.rav.valueAggregate;
            uint256 tokensAlreadyCollected = tokensCollected[dataService][collectionId][receiver][signedRAV.rav.payer];
            require(
                tokensRAV > tokensAlreadyCollected,
                GraphTallyCollectorInconsistentRAVTokens(tokensRAV, tokensAlreadyCollected)
            );

            if (_tokensToCollect == 0) {
                tokensToCollect = tokensRAV - tokensAlreadyCollected;
            } else {
                require(
                    _tokensToCollect <= tokensRAV - tokensAlreadyCollected,
                    GraphTallyCollectorInvalidTokensToCollectAmount(
                        _tokensToCollect,
                        tokensRAV - tokensAlreadyCollected
                    )
                );
                tokensToCollect = _tokensToCollect;
            }
        }

        if (tokensToCollect > 0) {
            tokensCollected[dataService][collectionId][receiver][signedRAV.rav.payer] += tokensToCollect;
            _graphPaymentsEscrow().collect(
                _paymentType,
                signedRAV.rav.payer,
                receiver,
                tokensToCollect,
                dataService,
                dataServiceCut,
                receiverDestination
            );
        }

        emit PaymentCollected(_paymentType, collectionId, signedRAV.rav.payer, receiver, dataService, tokensToCollect);

        emit RAVCollected(
            collectionId,
            signedRAV.rav.payer,
            receiver,
            dataService,
            signedRAV.rav.timestampNs,
            signedRAV.rav.valueAggregate,
            signedRAV.rav.metadata,
            signedRAV.signature
        );

        return tokensToCollect;
    }

    function _recoverRAVSigner(SignedRAV memory _signedRAV) private view returns (address) {
        bytes32 messageHash = _encodeRAV(_signedRAV.rav);
        return ECDSA.recover(messageHash, _signedRAV.signature);
    }

    function _encodeRAV(ReceiptAggregateVoucher memory _rav) private view returns (bytes32) {
        return
            _hashTypedDataV4(
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

    function _requireAuthorizedSigner(SignedRAV memory _signedRAV) private view {
        require(
            _isAuthorized(_signedRAV.rav.payer, _recoverRAVSigner(_signedRAV)),
            GraphTallyCollectorInvalidRAVSigner()
        );
    }
}

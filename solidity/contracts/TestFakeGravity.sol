//SPDX-License-Identifier: Apache-2.0
pragma solidity 0.8.16;

import "hardhat/console.sol";
import "./Gravity.sol";

// This test contract is used in conjuction with GravityERC721.sol to demonstrate
// that only Gravity.sol can call GravityERC721.sol. This fake contract will
// not be able to call
contract TestFakeGravity is Gravity {
	constructor(
		bytes32 _gravityId,
		address[] memory _validators,
		uint256[] memory _powers,
		address _adminAddress
	) Gravity(_gravityId, _validators, _powers, _adminAddress) {}
}

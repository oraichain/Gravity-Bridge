package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/gogo/protobuf/proto"
)

func (m Attestation) ValidateBasic(cdc codec.BinaryCodec) error {
	if m.Observed && len(m.Votes) == 0 {
		return sdkerrors.Wrap(ErrInvalidAttestation, "must be voted on to be observed")
	}

	for _, validator := range m.Votes {
		_, err := sdk.ValAddressFromBech32(validator)
		if err != nil {
			return sdkerrors.Wrap(ErrInvalidAttestation, "votes must contain bech32 validator addresses")
		}
	}

	err := ClaimValidateBasic(cdc, m.Claim)
	if err != nil {
		return sdkerrors.Wrap(ErrInvalidAttestation, err.Error())
	}
	return nil
}

func ClaimValidateBasic(cdc codec.BinaryCodec, claim *codectypes.Any) error {
	var ethClaim EthereumClaim
	err := cdc.UnpackAny(claim, &ethClaim)
	if err != nil {
		return sdkerrors.Wrap(ErrInvalidClaim, "unable to unmarshal claim")
	}
	if ethClaim == nil {
		return sdkerrors.Wrap(ErrInvalidClaim, "decoded nil claim")
	}

	if ethClaim.GetEvmChainPrefix() == "" {
		return sdkerrors.Wrap(ErrInvalidClaim, "evm_chain_prefix is empty")
	}

	// Returns nil on no error from ValidateBasic
	err = ethClaim.ValidateBasic()
	if err != nil {
		return sdkerrors.Wrap(ErrInvalidClaim, err.Error())
	}
	return nil
}

// ClaimTypeToTypeUrl takes a type of EthereumClaim and returns the associated protobuf Msg TypeUrl
func ClaimTypeToTypeUrl(claimType ClaimType) string {
	var msgName string
	switch claimType {
	case CLAIM_TYPE_UNSPECIFIED:
		return "unspecified"
	case CLAIM_TYPE_SEND_TO_COSMOS:
		msgName = proto.MessageName(&MsgSendToCosmosClaim{})
	case CLAIM_TYPE_BATCH_SEND_TO_ETH:
		msgName = proto.MessageName(&MsgBatchSendToEthClaim{})
	case CLAIM_TYPE_ERC20_DEPLOYED:
		msgName = proto.MessageName(&MsgERC20DeployedClaim{})
	case CLAIM_TYPE_LOGIC_CALL_EXECUTED:
		msgName = proto.MessageName(&MsgLogicCallExecutedClaim{})
	case CLAIM_TYPE_VALSET_UPDATED:
		msgName = proto.MessageName(&MsgValsetUpdatedClaim{})
	}

	return "/" + msgName
}

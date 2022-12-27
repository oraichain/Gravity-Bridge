package keeper

import (
	"fmt"
	"regexp"

	"github.com/Gravity-Bridge/Gravity-Bridge/module/x/gravity/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	capabilitytypes "github.com/cosmos/cosmos-sdk/x/capability/types"
	transfertypes "github.com/cosmos/ibc-go/v3/modules/apps/transfer/types"
	channeltypes "github.com/cosmos/ibc-go/v3/modules/core/04-channel/types"

	ibcexported "github.com/cosmos/ibc-go/v3/modules/core/exported"
)

// IsModuleAccount returns true if the given account is a module account
func IsModuleAccount(acc authtypes.AccountI) bool {
	_, isModuleAccount := acc.(authtypes.ModuleAccountI)
	return isModuleAccount
}

// GetReceivedCoin returns the transferred coin from an ICS20 FungibleTokenPacketData
// as seen from the destination chain.
// If the receiving chain is the source chain of the tokens, it removes the prefix
// path added by source (i.e sender) chain to the denom. Otherwise, it adds the
// prefix path from the destination chain to the denom.
func GetReceivedCoin(srcPort, srcChannel, dstPort, dstChannel, rawDenom, rawAmt string) sdk.Coin {
	// NOTE: Denom and amount are already validated
	amount, _ := sdk.NewIntFromString(rawAmt)

	if transfertypes.ReceiverChainIsSource(srcPort, srcChannel, rawDenom) {
		// remove prefix added by sender chain
		voucherPrefix := transfertypes.GetDenomPrefix(srcPort, srcChannel)
		unprefixedDenom := rawDenom[len(voucherPrefix):]

		// coin denomination used in sending from the escrow address
		denom := unprefixedDenom

		// The denomination used to send the coins is either the native denom or the hash of the path
		// if the denomination is not native.
		denomTrace := transfertypes.ParseDenomTrace(unprefixedDenom)
		if denomTrace.Path != "" {
			denom = denomTrace.IBCDenom()
		}

		return sdk.Coin{
			Denom:  denom,
			Amount: amount,
		}
	}

	// since SendPacket did not prefix the denomination, we must prefix denomination here
	sourcePrefix := transfertypes.GetDenomPrefix(dstPort, dstChannel)
	// NOTE: sourcePrefix contains the trailing "/"
	prefixedDenom := sourcePrefix + rawDenom

	// construct the denomination trace from the full raw denomination
	denomTrace := transfertypes.ParseDenomTrace(prefixedDenom)
	voucherDenom := denomTrace.IBCDenom()

	return sdk.Coin{
		Denom:  voucherDenom,
		Amount: amount,
	}
}

var memoSendToEthRegexp = regexp.MustCompile(`^([a-zA-Z][a-zA-Z0-9_-]+)0x[0-9a-fA-F]{40}$`)

// OnRecvPacket performs the ICS20 middleware receive callback for automatically
// converting an IBC Coin to their ERC20 representation.
// For the conversion to succeed, the IBC denomination must have previously been
// registered via governance. Note that the native staking denomination (e.g. "aevmos"),
// is excluded from the conversion.
//
// CONTRACT: This middleware MUST be executed transfer after the ICS20 OnRecvPacket
// Return acknowledgement and continue with the next layer of the IBC middleware
// stack if:
// - memo is not MsgSendToEth
// - The base denomination is not registered as ERC20
func (k Keeper) OnRecvPacket(
	ctx sdk.Context,
	packet channeltypes.Packet,
	ack ibcexported.Acknowledgement,
) ibcexported.Acknowledgement {
	// must success to be here
	var data transfertypes.FungibleTokenPacketData
	transfertypes.ModuleCdc.UnmarshalJSON(packet.GetData(), &data)
	// nothing to do
	if len(data.Memo) == 0 {
		return ack
	}

	// check memo format
	var match []string
	if match = memoSendToEthRegexp.FindStringSubmatch(data.Memo); len(match) == 0 {
		return ack
	}
	evmChainPrefix := match[1]
	ethDest := data.Memo[len(evmChainPrefix):]

	// Receiver become sender when send evm_prefix + contract_address token to evm
	sender, err := sdk.AccAddressFromBech32(data.Receiver)
	if err != nil {
		return channeltypes.NewErrorAcknowledgement(err.Error())
	}

	senderAcc := k.accountKeeper.GetAccount(ctx, sender)

	// return acknoledgement without conversion if sender is a module account
	if IsModuleAccount(senderAcc) {
		return ack
	}

	// parse the transferred denom
	coin := GetReceivedCoin(
		packet.SourcePort, packet.SourceChannel,
		packet.DestinationPort, packet.DestinationChannel,
		data.Denom, data.Amount,
	)

	dest, err := types.NewEthAddress(ethDest)
	if err != nil {
		return channeltypes.NewErrorAcknowledgement(err.Error())
	}
	_, erc20, err := k.DenomToERC20Lookup(ctx, evmChainPrefix, coin.Denom)
	if err != nil {
		return channeltypes.NewErrorAcknowledgement(err.Error())
	}

	if k.InvalidSendToEthAddress(ctx, *dest, *erc20) {
		return channeltypes.NewErrorAcknowledgement(sdkerrors.Wrap(types.ErrInvalid, "destination address is invalid or blacklisted").Error())
	}

	// finally add to outgoing pool and waiting for gbt to submit it via MsgRequestBatch
	txID, err := k.AddToOutgoingPool(ctx, evmChainPrefix, sender, *dest, coin, sdk.Coin{Denom: coin.Denom, Amount: sdk.ZeroInt()})
	if err != nil {
		return channeltypes.NewErrorAcknowledgement(err.Error())
	}

	ctx.EventManager().EmitTypedEvent(
		&types.EventOutgoingTxId{
			Message: "send_to_eth",
			TxId:    fmt.Sprint(txID),
		},
	)

	return ack
}

// SendPacket wraps IBC ChannelKeeper's SendPacket function
func (k Keeper) SendPacket(ctx sdk.Context, chanCap *capabilitytypes.Capability, packet ibcexported.PacketI) error {
	return k.ics4Wrapper.SendPacket(ctx, chanCap, packet)
}

// WriteAcknowledgement writes the packet execution acknowledgement to the state,
// which will be verified by the counterparty chain using AcknowledgePacket.
func (k Keeper) WriteAcknowledgement(ctx sdk.Context,
	chanCap *capabilitytypes.Capability,
	packet ibcexported.PacketI,
	ack ibcexported.Acknowledgement) error {
	return k.ics4Wrapper.WriteAcknowledgement(ctx, chanCap, packet, ack)
}

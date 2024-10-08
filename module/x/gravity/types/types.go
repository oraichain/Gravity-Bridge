package types

import (
	"crypto/md5"
	"encoding/binary"
	fmt "fmt"
	"strconv"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	bech32ibckeeper "github.com/althea-net/bech32-ibc/x/bech32ibc/keeper"
)

// UInt64FromBytesUnsafe create uint from binary big endian representation
// Note: This is unsafe because the function will panic if provided over 8 bytes
func UInt64FromBytesUnsafe(s []byte) uint64 {
	if len(s) > 8 {
		panic("Invalid uint64 bytes passed to UInt64FromBytes!")
	}
	return binary.BigEndian.Uint64(s)
}

// UInt64Bytes uses the SDK byte marshaling to encode a uint64
func UInt64Bytes(n uint64) []byte {
	return sdk.Uint64ToBigEndian(n)
}

// UInt64FromString to parse out a uint64 for a nonce
func UInt64FromString(s string) (uint64, error) {
	return strconv.ParseUint(s, 10, 64)
}

// IBCAddressFromBech32 decodes an IBC-compatible Address from a Bech32
// encoded string
func IBCAddressFromBech32(bech32str string) ([]byte, error) {
	if len(bech32str) == 0 {
		return nil, ErrEmpty
	}

	_, bz, err := bech32.DecodeAndConvert(bech32str)
	if err != nil {
		return nil, err
	}

	err = sdk.VerifyAddressFormat(bz)
	if err != nil {
		return nil, err
	}

	return bz, nil
}

// GetPrefixFromBech32 returns the human readable part of a bech32 string (excluding the 1 byte)
// Returns an error on too short input or when the 1 byte cannot be found
// Note: This is an excerpt from the Decode function for bech32 strings
func GetPrefixFromBech32(bech32str string) (string, error) {
	if len(bech32str) < 8 {
		return "", fmt.Errorf("invalid bech32 string length %d",
			len(bech32str))
	}
	one := strings.LastIndexByte(bech32str, '1')
	if one < 1 || one+7 > len(bech32str) {
		return "", fmt.Errorf("invalid index of 1")
	}

	return bech32str[:one], nil
}

// GetNativePrefixedAccAddressString treats the input as an AccAddress and re-prefixes the string
// with this chain's configured Bech32AccountAddrPrefix
// Returns an error when input is not a bech32 string
func GetNativePrefixedAccAddressString(ctx sdk.Context, bech32IbcKeeper bech32ibckeeper.Keeper, foreignStr string) (string, error) {
	prefix, err := GetPrefixFromBech32(foreignStr)
	if err != nil {
		return "", sdkerrors.Wrap(err, "invalid bech32 string")
	}
	nativePrefix, err := bech32IbcKeeper.GetNativeHrp(ctx)
	if err != nil {
		panic(sdkerrors.Wrap(err, "bech32ibc NativePrefix has not been registered!"))
	}
	if prefix == nativePrefix {
		return foreignStr, nil
	}

	return nativePrefix + foreignStr[len(prefix):], nil
}

// GetNativePrefixedAccAddress re-prefixes the input AccAddress with the registered bech32ibc NativeHrp
func GetNativePrefixedAccAddress(ctx sdk.Context, bech32IbcKeeper bech32ibckeeper.Keeper, foreignAddr sdk.AccAddress) (sdk.AccAddress, error) {
	nativeStr, err := GetNativePrefixedAccAddressString(ctx, bech32IbcKeeper, foreignAddr.String())
	if err != nil {
		return nil, err
	}
	return sdk.AccAddressFromBech32(nativeStr)
}

// Hashing string using cryptographic MD5 function
// returns 128bit(16byte) value
func HashString(input string) []byte {
	md5 := md5.New()
	md5.Write([]byte(input))
	return md5.Sum(nil)
}

func AppendBytes(args ...[]byte) []byte {
	length := 0
	for _, v := range args {
		length += len(v)
	}

	res := make([]byte, length)

	length = 0
	for _, v := range args {
		copy(res[length:length+len(v)], v)
		length += len(v)
	}

	return res
}

// ParseReceiver return channel, receiver, denom and error when validating receiver
// a:b:c => sourceChannel:destChannel/cosmosReceiver:denom
// a:b => sourceChannel:destChannel/cosmosReceiver
// a => sourceChannel/cosmosReceiver
// hrp is used when there is no source channel, otherwise it can be ignored
// destReceiver is used for validating, and will be pass to ibc wasm
func ParseReceiver(receiver string) (sourceChannel, cosmosReceiver, destination, accountPrefix string, receiverAddress []byte, err error) {
	sourceChannel, cosmosReceiver, destination = ParseReceiverRaw(receiver)
	accountPrefix, receiverAddress, err = bech32.DecodeAndConvert(cosmosReceiver)
	return
}

// ParseReceiverRaw return source channel & destination when parsing msg cosmos receiver
// a:b:c => sourceChannel=a, destination=b:c
// a:b => sourceChannel=a, destination=b
// a => if sourceChannel/cosmosReceiver then sourceChannel=a, destination=b. else sourceChannel="", destination=a
func ParseReceiverRaw(receiver string) (sourceChannel, cosmosReceiver, destination string) {
	args := strings.SplitN(receiver, ":", 2)
	if len(args) != 1 {
		sourceChannel, destination = args[0], args[1]
		if ind := strings.Index(sourceChannel, "/"); ind != -1 {
			sourceChannel, cosmosReceiver = sourceChannel[0:ind], sourceChannel[ind+1:]
		}
	} else {
		// source Receiver is destination
		if ind := strings.Index(receiver, "/"); ind != -1 {
			sourceChannel, cosmosReceiver = receiver[0:ind], receiver[ind+1:]
		} else {
			cosmosReceiver = receiver
		}
		destination = cosmosReceiver
	}

	return
}

// // channel/sender:channel/receiver:denom
// func ParseDestinationRaw(destination string) (receiver, destChannel, denom string, isCosmos bool) {
// 	isCosmos = true
// 	receiver = destination
// 	// has destination denom
// 	if ind := strings.Index(receiver, ":"); ind != -1 {
// 		receiver, denom = receiver[0:ind], receiver[ind+1:]
// 	}
// 	// now processing receiver
// 	if ind := strings.Index(receiver, "/"); ind != -1 {
// 		// cosmos style
// 		destChannel, receiver = receiver[0:ind], receiver[ind+1:]
// 	} else if ind := strings.Index(receiver, "0x"); ind != -1 {
// 		// ethereum style
// 		destChannel, receiver = receiver[0:ind], receiver[ind:]
// 		isCosmos = false
// 	}
// 	return
// }

// func ParseDestination(destination string) (receiver []byte, destChannel, denom, hrp string, err error) {
// 	destination, destChannel, denom, isCosmos := ParseDestinationRaw(destination)
// 	if isCosmos {
// 		// validate cosmos
// 		hrp, receiver, err = bech32.DecodeAndConvert(destination)
// 	} else {
// 		// validate ethereum
// 		var ethAddress *EthAddress
// 		ethAddress, err = NewEthAddress(destination)
// 		receiver = ethAddress.address[:]
// 	}
// 	return
// }

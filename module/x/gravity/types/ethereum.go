package types

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	gethcommon "github.com/ethereum/go-ethereum/common"
	"golang.org/x/exp/slices"
)

const (
	// GravityDenomPrefix indicates the prefix for all assets minted by this module
	GravityDenomPrefix = "oraib"

	GravityERC721ClassIDPrefix = ModuleName + "erc721"

	// GravityDenomSeparator is the separator for gravity denoms
	GravityDenomSeparator = ""

	// ETHContractAddressLen is the length of contract address strings
	ETHContractAddressLen = 42

	// GravityDenomLenMin is the min length of the denoms generated by the gravity module
	GravityDenomLenMin = 1 + len(GravityDenomSeparator) + ETHContractAddressLen

	// ZeroAddress is an EthAddress containing the zero ethereum address
	ZeroAddressString = "0x0000000000000000000000000000000000000000"
)

// Regular EthAddress
type EthAddress struct {
	address gethcommon.Address
}

// Returns the contained address as a string
func (ea EthAddress) GetAddress() gethcommon.Address {
	return ea.address
}

// Sets the contained address, performing validation before updating the value
func (ea *EthAddress) SetAddress(address string) error {
	if err := ValidateEthAddress(address); err != nil {
		return err
	}
	ea.address = gethcommon.HexToAddress(address)
	return nil
}

func NewEthAddressFromBytes(address []byte) (*EthAddress, error) {

	if err := ValidateEthAddress(hex.EncodeToString(address)); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid input address")
	}

	addr := EthAddress{gethcommon.BytesToAddress(address)}
	return &addr, nil
}

// Creates a new EthAddress from a string, performing validation and returning any validation errors
func NewEthAddress(address string) (*EthAddress, error) {
	if err := ValidateEthAddress(address); err != nil {
		return nil, sdkerrors.Wrap(err, "invalid input address")
	}

	addr := EthAddress{gethcommon.HexToAddress(address)}
	return &addr, nil
}

func EthAddressFromCommon(address gethcommon.Address) *EthAddress {
	return &EthAddress{address}
}

// Returns a new EthAddress with 0x0000000000000000000000000000000000000000 as the wrapped address
func ZeroAddress() EthAddress {
	return EthAddress{gethcommon.HexToAddress(ZeroAddressString)}
}

// Validates the input string as an Ethereum Address
// Addresses must not be empty, have 42 character length, start with 0x and have 40 remaining characters in [0-9a-fA-F]
func ValidateEthAddress(address string) error {

	if address == "" {
		return fmt.Errorf("empty")
	}

	// An auditor recommended we should check the error of hex.DecodeString, given that geth's HexToAddress ignores it

	if has0xPrefix(address) {
		address = address[2:]
	}

	if _, err := hex.DecodeString(address); err != nil {
		return fmt.Errorf("invalid hex with error: %s", err)
	}

	if !gethcommon.IsHexAddress(address) {
		return fmt.Errorf("address(%s) doesn't pass format validation", address)
	}

	return nil
}

// Performs validation on the wrapped string
func (ea EthAddress) ValidateBasic() error {
	return ValidateEthAddress(ea.address.Hex())
}

// EthAddrLessThan migrates the Ethereum address less than function
func EthAddrLessThan(e EthAddress, o EthAddress) bool {
	return bytes.Compare([]byte(e.GetAddress().Hex()), []byte(o.GetAddress().Hex())) == -1
}

type EthAddresses []EthAddress

func FromMonitoredERC20Addresses(in MonitoredERC20Addresses) EthAddresses {
	var eas []EthAddress
	for _, a := range in.Addresses {
		ea, err := NewEthAddressFromBytes(a)
		if err != nil {
			panic(fmt.Sprintf("Invalid address in MonitoredERC20Addresses: %s", err.Error()))
		}
		eas = append(eas, *ea)
	}
	return eas
}

func (eas EthAddresses) ToMonitoredERC20Addresses() MonitoredERC20Addresses {
	var byteses [][]byte
	for _, ea := range eas {
		byteses = append(byteses, ea.GetAddress().Bytes())
	}
	return MonitoredERC20Addresses{Addresses: byteses}
}

/////////////////////////
// ERC20Token ///////////
/////////////////////////

// NewERC20Token returns a new instance of an ERC20
func NewERC20Token(amount uint64, contract string) ERC20Token {
	return ERC20Token{Amount: sdk.NewIntFromUint64(amount), Contract: contract}
}

// NewSDKIntERC20Token returns a new instance of an ERC20, accepting a sdk.Int
func NewSDKIntERC20Token(amount sdk.Int, contract string) ERC20Token {
	return ERC20Token{Amount: amount, Contract: contract}
}

// ToInternal converts an ERC20Token to the internal type InternalERC20Token
func (e ERC20Token) ToInternal() (*InternalERC20Token, error) {
	return NewInternalERC20Token(e.Amount, e.Contract)
}

// InternalERC20Token contains validated fields, used for all internal computation
type InternalERC20Token struct {
	Amount   sdk.Int
	Contract EthAddress
}

// NewInternalERC20Token creates an InternalERC20Token, performing validation and returning any errors
func NewInternalERC20Token(amount sdk.Int, contract string) (*InternalERC20Token, error) {
	ethAddress, err := NewEthAddress(contract)
	if err != nil { // ethAddress could be nil, must return here
		return nil, sdkerrors.Wrap(err, "invalid contract")
	}
	ret := &InternalERC20Token{
		Amount:   amount,
		Contract: *ethAddress,
	}
	if err := ret.ValidateBasic(); err != nil {
		return nil, err
	}

	return ret, nil
}

// ValidateBasic performs validation on all fields of an InternalERC20Token
func (i *InternalERC20Token) ValidateBasic() error {
	err := i.Contract.ValidateBasic()
	if err != nil {
		return sdkerrors.Wrap(err, "invalid contract")
	}
	return nil
}

// ToExternal converts an InternalERC20Token to the external type ERC20Token
func (i *InternalERC20Token) ToExternal() ERC20Token {
	return ERC20Token{
		Amount:   i.Amount,
		Contract: i.Contract.GetAddress().Hex(),
	}
}

// GravityCoin returns the gravity representation of the ERC20
func (i *InternalERC20Token) GravityCoin(evmChainPrefix string) sdk.Coin {
	return sdk.NewCoin(GravityDenom(evmChainPrefix, i.Contract), i.Amount)
}

// GravityDenom converts an EthAddress to a gravity cosmos denom
func GravityDenom(evmChainPrefix string, tokenContract EthAddress) string {
	return fmt.Sprintf("%s%s%s", evmChainPrefix, GravityDenomSeparator, tokenContract.GetAddress().Hex())
}

// GravityERC721ClassId converts an EthAddress to a gravity cosmos class id for ERC721 tokens
func GravityERC721ClassId(tokenContract EthAddress) string {
	return fmt.Sprintf("%s%s%s", GravityERC721ClassIDPrefix, GravityDenomSeparator, tokenContract.GetAddress().Hex())
}

// ValidateBasic performs stateless validation
func (e *ERC20Token) ValidateBasic() error {
	if err := ValidateEthAddress(e.Contract); err != nil {
		return sdkerrors.Wrap(err, "ethereum address")
	}
	// TODO: Validate all the things
	return nil
}

// Add adds one ERC20 to another
func (i *InternalERC20Token) Add(o *InternalERC20Token) (*InternalERC20Token, error) {
	if i.Contract.GetAddress() != o.Contract.GetAddress() {
		return nil, sdkerrors.Wrap(ErrMismatched, "cannot add two different tokens")
	}
	sum := i.Amount.Add(o.Amount) // validation happens in NewInternalERC20Token()
	return NewInternalERC20Token(sum, i.Contract.GetAddress().Hex())
}

// Neg changes the sign of i
func (i *InternalERC20Token) Neg() {
	i.Amount = i.Amount.Neg()
}

type ERC20Tokens []*ERC20Token

// ToInternal converts an ERC20Token to the internal type InternalERC20Token
func (e ERC20Tokens) ToInternal() (InternalERC20Tokens, error) {
	var result []*InternalERC20Token
	for _, t := range e {
		i, err := t.ToInternal()
		if err != nil {
			return nil, fmt.Errorf("unable to convert token (%v) to internal type: %v", t, err)
		}
		result = append(result, i)
	}
	return result, nil
}

type InternalERC20Tokens []*InternalERC20Token

func (i InternalERC20Tokens) IsSorted() bool {
	for j := range i {
		if j == len(i)-1 {
			break
		}
		if i[j].Contract.GetAddress().String() >= i[j+1].Contract.GetAddress().String() {
			return false
		}
	}
	return true
}

func (i *InternalERC20Tokens) Add(n InternalERC20Token) {
	for _, token := range *i {
		if token.Contract.GetAddress() == n.Contract.GetAddress() {
			token.Amount = token.Amount.Add(n.Amount)
			return
		}
	}
	// Never found an entry with n's contract, add it to the end of the collection
	*i = append(*i, &n)
}

// Sort orders the elements of i by contract address
func (i InternalERC20Tokens) Sort() {
	slices.SortFunc(i, func(a, b *InternalERC20Token) bool {
		return a.Contract.GetAddress().String() < b.Contract.GetAddress().String()
	})
}

// Neg changes the sign of each element of i
func (i InternalERC20Tokens) Neg() {
	for _, t := range i {
		t.Neg()
	}
}

// SubSorted will perform subtraction between two sets of sorted InternalERC20Tokens
// If any final amounts are zero, they will not be included in the result
func (i InternalERC20Tokens) SubSorted(o InternalERC20Tokens) InternalERC20Tokens {
	other := make(InternalERC20Tokens, len(o))
	copy(other, o)
	other.Neg()
	return i.AddSorted(other)
}

// AddSorted will perform addition between two sets of sorted InternalERC20Tokens
// If any final amounts are zero, they will not be included in the result
func (i InternalERC20Tokens) AddSorted(o InternalERC20Tokens) InternalERC20Tokens {
	if !i.IsSorted() || !o.IsSorted() {
		panic("inputs to AddSorted are not sorted!")
	}

	unique := make(map[EthAddress]InternalERC20Tokens, len(i)+len(o))
	// Add each token to a list of tokens based on contract
	for _, list := range []InternalERC20Tokens{i, o} {
		for _, token := range list {
			unique[token.Contract] = append(unique[token.Contract], token)
		}
	}

	var result InternalERC20Tokens
	for ctr, list := range unique {
		ctrTotal := &InternalERC20Token{Contract: ctr, Amount: sdk.NewInt(0)}
		for _, token := range list {
			var err error
			ctrTotal, err = ctrTotal.Add(token)
			if err != nil {
				panic(err)
			}
		}
		if !ctrTotal.Amount.IsZero() {
			result = append(result, ctrTotal)
		}
	}
	return result
}

// ToCoins converts each InternalERC20Token to an sdk.Coin by calling GravityCoin()
func (i InternalERC20Tokens) ToCoins(evmChainPrefix string) sdk.Coins {
	var coins sdk.Coins
	for _, v := range i {
		coins = coins.Add(v.GravityCoin(evmChainPrefix))
	}
	return coins
}

// GravityDenomToERC20 converts a gravity cosmos denom to an EthAddress
func GravityDenomToERC20(evmChainPrefix string, denom string) (*EthAddress, error) {
	fullPrefix := evmChainPrefix + GravityDenomSeparator
	if !strings.HasPrefix(denom, fullPrefix) {
		return nil, fmt.Errorf("denom prefix(%s) not equal to expected(%s)", denom, fullPrefix)
	}
	contract := strings.TrimPrefix(denom, fullPrefix)
	ethAddr, err := NewEthAddress(contract)
	switch {
	case err != nil:
		return nil, fmt.Errorf("error(%s) validating ethereum contract address", err)
	case len(denom) <= GravityDenomLenMin:
		return nil, fmt.Errorf("len(denom)(%d) smaller than GravityDenomLen(%d)", len(denom), GravityDenomLenMin)
	default:
		return ethAddr, nil
	}
}

func has0xPrefix(str string) bool {
	return len(str) >= 2 && str[0] == '0' && (str[1] == 'x' || str[1] == 'X')
}

func (m ERC20ToDenom) ValidateBasic() error {
	trimDenom := strings.TrimSpace(m.Denom)
	if trimDenom == "" || trimDenom != m.Denom {
		return sdkerrors.Wrap(ErrInvalid, "invalid erc20todenom: denom must be properly formatted")
	}
	trimErc20 := strings.TrimSpace(m.Erc20)
	if trimErc20 == "" || trimErc20 != m.Erc20 {
		return sdkerrors.Wrap(ErrInvalid, "invalid erc20todenom: erc20 must be properly formatted")
	}
	addr, err := NewEthAddress(m.Erc20)
	if err != nil {
		return sdkerrors.Wrapf(ErrInvalid, "invalid erc20todenom: erc20 must be a valid ethereum address: %v", err)
	}
	if err = addr.ValidateBasic(); err != nil {
		return sdkerrors.Wrapf(ErrInvalid, "invalid erc20todenom: erc20 address failed validate basic: %v", err)
	}
	if err = sdk.ValidateDenom(m.Denom); err != nil {
		return sdkerrors.Wrapf(ErrInvalid, "invalid erc20todenom: denom is invalid: %v", err)
	}

	return nil
}

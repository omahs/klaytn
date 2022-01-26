// Copyright 2021 The klaytn Authors
// This file is part of the klaytn library.
//
// The klaytn library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The klaytn library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the klaytn library. If not, see <http://www.gnu.org/licenses/>.

package api

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/klaytn/klaytn/blockchain"
	"github.com/klaytn/klaytn/blockchain/state"
	"github.com/klaytn/klaytn/blockchain/vm"
	"github.com/klaytn/klaytn/common/math"
	"math/big"
	"sync/atomic"
	"time"

	"github.com/klaytn/klaytn/blockchain/types"
	"github.com/klaytn/klaytn/common"
	"github.com/klaytn/klaytn/common/hexutil"
	"github.com/klaytn/klaytn/governance"
	"github.com/klaytn/klaytn/networks/rpc"
	"github.com/klaytn/klaytn/node/cn/filters"
	"github.com/klaytn/klaytn/params"
)

const (
	// EmptySha3Uncles always have value which is the result of
	// `crypto.Keccak256Hash(rlp.EncodeToBytes([]*types.Header(nil)).String())`
	// because there is no uncles in Klaytn.
	// Just use const value because we don't have to calculate it everytime which always be same result.
	EmptySha3Uncles = "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347"
	// DummyGasLimit exists for supporting Ethereum compatible data structure.
	// There is no gas limit mechanism in Klaytn, check details in https://docs.klaytn.com/klaytn/design/computation/computation-cost.
	DummyGasLimit uint64 = 999999999
)

// EthereumAPI provides an API to access the Klaytn through the `eth` namespace.
// TODO-Klaytn: Removed unused variable
type EthereumAPI struct {
	publicFilterAPI   *filters.PublicFilterAPI
	governanceKlayAPI *governance.GovernanceKlayAPI

	publicKlayAPI            *PublicKlayAPI
	publicBlockChainAPI      *PublicBlockChainAPI
	publicTransactionPoolAPI *PublicTransactionPoolAPI
	publicAccountAPI         *PublicAccountAPI
}

// NewEthereumAPI creates a new ethereum API.
// EthereumAPI operates using Klaytn's API internally without overriding.
// Therefore, it is necessary to use APIs defined in two different packages(cn and api),
// so those apis will be defined through a setter.
func NewEthereumAPI() *EthereumAPI {
	return &EthereumAPI{nil, nil, nil, nil, nil, nil}
}

// SetPublicFilterAPI sets publicFilterAPI
func (api *EthereumAPI) SetPublicFilterAPI(publicFilterAPI *filters.PublicFilterAPI) {
	api.publicFilterAPI = publicFilterAPI
}

// SetGovernanceKlayAPI sets governanceKlayAPI
func (api *EthereumAPI) SetGovernanceKlayAPI(governanceKlayAPI *governance.GovernanceKlayAPI) {
	api.governanceKlayAPI = governanceKlayAPI
}

// SetPublicKlayAPI sets publicKlayAPI
func (api *EthereumAPI) SetPublicKlayAPI(publicKlayAPI *PublicKlayAPI) {
	api.publicKlayAPI = publicKlayAPI
}

// SetPublicBlockChainAPI sets publicBlockChainAPI
func (api *EthereumAPI) SetPublicBlockChainAPI(publicBlockChainAPI *PublicBlockChainAPI) {
	api.publicBlockChainAPI = publicBlockChainAPI
}

// SetPublicTransactionPoolAPI sets publicTransactionPoolAPI
func (api *EthereumAPI) SetPublicTransactionPoolAPI(publicTransactionPoolAPI *PublicTransactionPoolAPI) {
	api.publicTransactionPoolAPI = publicTransactionPoolAPI
}

// SetPublicAccountAPI sets publicAccountAPI
func (api *EthereumAPI) SetPublicAccountAPI(publicAccountAPI *PublicAccountAPI) {
	api.publicAccountAPI = publicAccountAPI
}

// Etherbase is the address that mining rewards will be send to.
func (api *EthereumAPI) Etherbase() (common.Address, error) {
	// TODO-Klaytn: Not implemented yet.
	return common.StringToAddress("0x0"), nil
}

// Coinbase is the address that mining rewards will be send to (alias for Etherbase).
func (api *EthereumAPI) Coinbase() (common.Address, error) {
	// TODO-Klaytn: Not implemented yet.
	return common.StringToAddress("0x0"), nil
}

// Hashrate returns the POW hashrate.
func (api *EthereumAPI) Hashrate() hexutil.Uint64 {
	// TODO-Klaytn: Not implemented yet.
	return 0
}

// Mining returns an indication if this node is currently mining.
func (api *EthereumAPI) Mining() bool {
	// TODO-Klaytn: Not implemented yet.
	return false
}

// GetWork returns a work package for external miner.
//
// The work package consists of 3 strings:
//   result[0] - 32 bytes hex encoded current block header pow-hash
//   result[1] - 32 bytes hex encoded seed hash used for DAG
//   result[2] - 32 bytes hex encoded boundary condition ("target"), 2^256/difficulty
//   result[3] - hex encoded block number
func (api *EthereumAPI) GetWork() ([4]string, error) {
	// TODO-Klaytn: Not implemented yet.
	return [4]string{}, nil
}

// A BlockNonce is a 64-bit hash which proves (combined with the
// mix-hash) that a sufficient amount of computation has been carried
// out on a block.
type BlockNonce [8]byte

// EncodeNonce converts the given integer to a block nonce.
func EncodeNonce(i uint64) BlockNonce {
	var n BlockNonce
	binary.BigEndian.PutUint64(n[:], i)
	return n
}

// Uint64 returns the integer value of a block nonce.
func (n BlockNonce) Uint64() uint64 {
	return binary.BigEndian.Uint64(n[:])
}

// MarshalText encodes n as a hex string with 0x prefix.
func (n BlockNonce) MarshalText() ([]byte, error) {
	return hexutil.Bytes(n[:]).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (n *BlockNonce) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("BlockNonce", input, n[:])
}

// SubmitWork can be used by external miner to submit their POW solution.
// It returns an indication if the work was accepted.
// Note either an invalid solution, a stale work a non-existent work will return false.
func (api *EthereumAPI) SubmitWork(nonce BlockNonce, hash, digest common.Hash) bool {
	// TODO-Klaytn: Not implemented yet.
	return false
}

// SubmitHashrate can be used for remote miners to submit their hash rate.
// This enables the node to report the combined hash rate of all miners
// which submit work through this node.
//
// It accepts the miner hash rate and an identifier which must be unique
// between nodes.
func (api *EthereumAPI) SubmitHashrate(rate hexutil.Uint64, id common.Hash) bool {
	// TODO-Klaytn: Not implemented yet.
	return false
}

// GetHashrate returns the current hashrate for local CPU miner and remote miner.
func (api *EthereumAPI) GetHashrate() uint64 {
	// TODO-Klaytn: Not implemented yet.
	return 0
}

// NewPendingTransactionFilter creates a filter that fetches pending transaction hashes
// as transactions enter the pending state.
//
// It is part of the filter package because this filter can be used through the
// `eth_getFilterChanges` polling method that is also used for log filters.
//
// https://eth.wiki/json-rpc/API#eth_newpendingtransactionfilter
func (api *EthereumAPI) NewPendingTransactionFilter() rpc.ID {
	// TODO-Klaytn: Not implemented yet.
	return ""
}

// NewPendingTransactions creates a subscription that is triggered each time a transaction
// enters the transaction pool and was signed from one of the transactions this nodes manages.
func (api *EthereumAPI) NewPendingTransactions(ctx context.Context) (*rpc.Subscription, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// NewBlockFilter creates a filter that fetches blocks that are imported into the chain.
// It is part of the filter package since polling goes with eth_getFilterChanges.
//
// https://eth.wiki/json-rpc/API#eth_newblockfilter
func (api *EthereumAPI) NewBlockFilter() rpc.ID {
	// TODO-Klaytn: Not implemented yet.
	return ""
}

// NewHeads send a notification each time a new (header) block is appended to the chain.
func (api *EthereumAPI) NewHeads(ctx context.Context) (*rpc.Subscription, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// Logs creates a subscription that fires for all new log that match the given filter criteria.
func (api *EthereumAPI) Logs(ctx context.Context, crit FilterCriteria) (*rpc.Subscription, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// FilterCriteria represents a request to create a new filter.
type FilterCriteria filters.FilterCriteria

// NewFilter creates a new filter and returns the filter id. It can be
// used to retrieve logs when the state changes. This method cannot be
// used to fetch logs that are already stored in the state.
//
// Default criteria for the from and to block are "latest".
// Using "latest" as block number will return logs for mined blocks.
// Using "pending" as block number returns logs for not yet mined (pending) blocks.
// In case logs are removed (chain reorg) previously returned logs are returned
// again but with the removed property set to true.
//
// In case "fromBlock" > "toBlock" an error is returned.
//
// https://eth.wiki/json-rpc/API#eth_newfilter
func (api *EthereumAPI) NewFilter(crit FilterCriteria) (rpc.ID, error) {
	// TODO-Klaytn: Not implemented yet.
	return "", nil
}

// GetLogs returns logs matching the given argument that are stored within the state.
//
// https://eth.wiki/json-rpc/API#eth_getlogs
func (api *EthereumAPI) GetLogs(ctx context.Context, crit FilterCriteria) ([]*types.Log, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// UninstallFilter removes the filter with the given filter id.
//
// https://eth.wiki/json-rpc/API#eth_uninstallfilter
func (api *EthereumAPI) UninstallFilter(id rpc.ID) bool {
	// TODO-Klaytn: Not implemented yet.
	return false
}

// GetFilterLogs returns the logs for the filter with the given id.
// If the filter could not be found an empty array of logs is returned.
//
// https://eth.wiki/json-rpc/API#eth_getfilterlogs
func (api *EthereumAPI) GetFilterLogs(ctx context.Context, id rpc.ID) ([]*types.Log, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetFilterChanges returns the logs for the filter with the given id since
// last time it was called. This can be used for polling.
//
// For pending transaction and block filters the result is []common.Hash.
// (pending)Log filters return []Log.
//
// https://eth.wiki/json-rpc/API#eth_getfilterchanges
func (api *EthereumAPI) GetFilterChanges(id rpc.ID) (interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GasPrice returns a suggestion for a gas price for legacy transactions.
func (api *EthereumAPI) GasPrice(ctx context.Context) (*hexutil.Big, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// MaxPriorityFeePerGas returns a suggestion for a gas tip cap for dynamic fee transactions.
func (api *EthereumAPI) MaxPriorityFeePerGas(ctx context.Context) (*hexutil.Big, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

type feeHistoryResult struct {
	OldestBlock  *hexutil.Big     `json:"oldestBlock"`
	Reward       [][]*hexutil.Big `json:"reward,omitempty"`
	BaseFee      []*hexutil.Big   `json:"baseFeePerGas,omitempty"`
	GasUsedRatio []float64        `json:"gasUsedRatio"`
}

// DecimalOrHex unmarshals a non-negative decimal or hex parameter into a uint64.
type DecimalOrHex uint64

func (api *EthereumAPI) FeeHistory(ctx context.Context, blockCount DecimalOrHex, lastBlock rpc.BlockNumber, rewardPercentiles []float64) (*feeHistoryResult, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// Syncing returns false in case the node is currently not syncing with the network. It can be up to date or has not
// yet received the latest block headers from its pears. In case it is synchronizing:
// - startingBlock: block number this node started to synchronise from
// - currentBlock:  block number this node is currently importing
// - highestBlock:  block number of the highest block header this node has received from peers
// - pulledStates:  number of state entries processed until now
// - knownStates:   number of known state entries that still need to be pulled
func (api *EthereumAPI) Syncing() (interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// ChainId is the EIP-155 replay-protection chain id for the current ethereum chain config.
func (api *EthereumAPI) ChainId() (*hexutil.Big, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// BlockNumber returns the block number of the chain head.
func (api *EthereumAPI) BlockNumber() hexutil.Uint64 {
	// TODO-Klaytn: Not implemented yet.
	return 0
}

// GetBalance returns the amount of wei for the given address in the state of the
// given block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta
// block numbers are also allowed.
func (api *EthereumAPI) GetBalance(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// EthAccountResult structs for GetProof
// AccountResult in go-ethereum has been renamed to EthAccountResult.
// AccountResult is defined in go-ethereum's internal package, so AccountResult is redefined here as EthAccountResult.
type EthAccountResult struct {
	Address      common.Address     `json:"address"`
	AccountProof []string           `json:"accountProof"`
	Balance      *hexutil.Big       `json:"balance"`
	CodeHash     common.Hash        `json:"codeHash"`
	Nonce        hexutil.Uint64     `json:"nonce"`
	StorageHash  common.Hash        `json:"storageHash"`
	StorageProof []EthStorageResult `json:"storageProof"`
}

// StorageResult in go-ethereum has been renamed to EthStorageResult.
// StorageResult is defined in go-ethereum's internal package, so StorageResult is redefined here as EthStorageResult.
type EthStorageResult struct {
	Key   string       `json:"key"`
	Value *hexutil.Big `json:"value"`
	Proof []string     `json:"proof"`
}

// GetProof returns the Merkle-proof for a given account and optionally some storage keys.
func (api *EthereumAPI) GetProof(ctx context.Context, address common.Address, storageKeys []string, blockNrOrHash rpc.BlockNumberOrHash) (*EthAccountResult, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetHeaderByNumber returns the requested canonical block header.
// * When blockNr is -1 the chain head is returned.
// * When blockNr is -2 the pending chain head is returned.
func (api *EthereumAPI) GetHeaderByNumber(ctx context.Context, number rpc.BlockNumber) (map[string]interface{}, error) {
	// In Ethereum, err is always nil because the backend of Ethereum always return nil.
	klaytnHeader, _ := api.publicBlockChainAPI.GetHeaderByNumber(ctx, number)
	if klaytnHeader != nil {
		response, err := api.rpcMarshalHeader(klaytnHeader)
		if err != nil {
			return nil, err
		}
		if number == rpc.PendingBlockNumber {
			// Pending header need to nil out a few fields
			for _, field := range []string{"hash", "nonce", "miner"} {
				response[field] = nil
			}
		}
		return response, nil
	}
	return nil, nil
}

// GetHeaderByHash returns the requested header by hash.
func (api *EthereumAPI) GetHeaderByHash(ctx context.Context, hash common.Hash) map[string]interface{} {
	// In Ethereum, err is always nil because the backend of Ethereum always return nil.
	klaytnHeader, _ := api.publicBlockChainAPI.GetHeaderByHash(ctx, hash)
	if klaytnHeader != nil {
		response, err := api.rpcMarshalHeader(klaytnHeader)
		if err != nil {
			return nil
		}
		return response
	}
	return nil
}

// GetBlockByNumber returns the requested canonical block.
// * When blockNr is -1 the chain head is returned.
// * When blockNr is -2 the pending chain head is returned.
// * When fullTx is true all transactions in the block are returned, otherwise
//   only the transaction hash is returned.
func (api *EthereumAPI) GetBlockByNumber(ctx context.Context, number rpc.BlockNumber, fullTx bool) (map[string]interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetBlockByHash returns the requested block. When fullTx is true all transactions in the block are returned in full
// detail, otherwise only the transaction hash is returned.
func (api *EthereumAPI) GetBlockByHash(ctx context.Context, hash common.Hash, fullTx bool) (map[string]interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetUncleByBlockNumberAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (api *EthereumAPI) GetUncleByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) (map[string]interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetUncleByBlockHashAndIndex returns the uncle block for the given block hash and index. When fullTx is true
// all transactions in the block are returned in full detail, otherwise only the transaction hash is returned.
func (api *EthereumAPI) GetUncleByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) (map[string]interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetUncleCountByBlockNumber returns number of uncles in the block for the given block number.
func (api *EthereumAPI) GetUncleCountByBlockNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetUncleCountByBlockHash returns number of uncles in the block for the given block hash.
func (api *EthereumAPI) GetUncleCountByBlockHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetCode returns the code stored at the given address in the state for the given block number.
func (api *EthereumAPI) GetCode(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetStorageAt returns the storage from the state at the given address, key and
// block number. The rpc.LatestBlockNumber and rpc.PendingBlockNumber meta block
// numbers are also allowed.
func (api *EthereumAPI) GetStorageAt(ctx context.Context, address common.Address, key string, blockNrOrHash rpc.BlockNumberOrHash) (hexutil.Bytes, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// EthOverrideAccount indicates the overriding fields of account during the execution
// of a message call.
// Note, state and stateDiff can't be specified at the same time. If state is
// set, message execution will only use the data in the given state. Otherwise
// if statDiff is set, all diff will be applied first and then execute the call
// message.
// OverrideAccount in go-ethereum has been renamed to EthOverrideAccount.
// OverrideAccount is defined in go-ethereum's internal package, so OverrideAccount is redefined here as EthOverrideAccount.
type EthOverrideAccount struct {
	Nonce     *hexutil.Uint64              `json:"nonce"`
	Code      *hexutil.Bytes               `json:"code"`
	Balance   **hexutil.Big                `json:"balance"`
	State     *map[common.Hash]common.Hash `json:"state"`
	StateDiff *map[common.Hash]common.Hash `json:"stateDiff"`
}

// EthStateOverride is the collection of overridden accounts.
// StateOverride in go-ethereum has been renamed to EthStateOverride.
// StateOverride is defined in go-ethereum's internal package, so StateOverride is redefined here as EthStateOverride.
type EthStateOverride map[common.Address]EthOverrideAccount

func (diff *EthStateOverride) Apply(state *state.StateDB) error {
	if diff == nil {
		return nil
	}
	for addr, account := range *diff {
		// Override account nonce.
		if account.Nonce != nil {
			state.SetNonce(addr, uint64(*account.Nonce))
		}
		// Override account(contract) code.
		if account.Code != nil {
			state.SetCode(addr, *account.Code)
		}
		// Override account balance.
		if account.Balance != nil {
			state.SetBalance(addr, (*big.Int)(*account.Balance))
		}
		if account.State != nil && account.StateDiff != nil {
			return fmt.Errorf("account %s has both 'state' and 'stateDiff'", addr.Hex())
		}
		// Replace entire state if caller requires.
		if account.State != nil {
			state.SetStorage(addr, *account.State)
		}
		// Apply state diff into specified accounts.
		if account.StateDiff != nil {
			for key, value := range *account.StateDiff {
				state.SetState(addr, key, value)
			}
		}
	}
	return nil
}

// Call executes the given transaction on the state for the given block number.
//
// Additionally, the caller can specify a batch of contract for fields overriding.
//
// Note, this function doesn't make and changes in the state/blockchain and is
// useful to execute and retrieve values.
func (api *EthereumAPI) Call(ctx context.Context, args EthTransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *EthStateOverride) (hexutil.Bytes, error) {
	gasCap := uint64(0)
	if rpcGasCap := api.publicBlockChainAPI.b.RPCGasCap(); rpcGasCap != nil {
		gasCap = rpcGasCap.Uint64()
	}
	result, _, err := EthDoCall(ctx, api.publicBlockChainAPI.b, args, blockNrOrHash, overrides, localTxExecutionTime, gasCap)
	return (hexutil.Bytes)(result), err
}

// EstimateGas returns an estimate of the amount of gas needed to execute the
// given transaction against the current pending block.
func (api *EthereumAPI) EstimateGas(ctx context.Context, args EthTransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (hexutil.Uint64, error) {
	bNrOrHash := rpc.NewBlockNumberOrHashWithNumber(rpc.LatestBlockNumber)
	if blockNrOrHash != nil {
		bNrOrHash = *blockNrOrHash
	}
	gasCap := uint64(0)
	if rpcGasCap := api.publicBlockChainAPI.b.RPCGasCap(); rpcGasCap != nil {
		gasCap = rpcGasCap.Uint64()
	}
	return EthDoEstimateGas(ctx, api.publicBlockChainAPI.b, args, bNrOrHash, gasCap)
}

// GetBlockTransactionCountByNumber returns the number of transactions in the block with the given block number.
func (api *EthereumAPI) GetBlockTransactionCountByNumber(ctx context.Context, blockNr rpc.BlockNumber) *hexutil.Uint {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetBlockTransactionCountByHash returns the number of transactions in the block with the given hash.
func (api *EthereumAPI) GetBlockTransactionCountByHash(ctx context.Context, blockHash common.Hash) *hexutil.Uint {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// accessListResult returns an optional accesslist
// Its the result of the `debug_createAccessList` RPC call.
// It contains an error if the transaction itself failed.
type accessListResult struct {
	Accesslist *AccessList    `json:"accessList"`
	Error      string         `json:"error,omitempty"`
	GasUsed    hexutil.Uint64 `json:"gasUsed"`
}

// AccessList is an EIP-2930 access list.
type AccessList []AccessTuple

// AccessTuple is the element type of an access list.
type AccessTuple struct {
	Address     common.Address `json:"address"        gencodec:"required"`
	StorageKeys []common.Hash  `json:"storageKeys"    gencodec:"required"`
}

// CreateAccessList creates a EIP-2930 type AccessList for the given transaction.
// Reexec and BlockNrOrHash can be specified to create the accessList on top of a certain state.
func (api *EthereumAPI) CreateAccessList(ctx context.Context, args EthTransactionArgs, blockNrOrHash *rpc.BlockNumberOrHash) (*accessListResult, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// EthRPCTransaction represents a transaction that will serialize to the RPC representation of a transaction
// RPCTransaction in go-ethereum has been renamed to EthRPCTransaction.
// RPCTransaction is defined in go-ethereum's internal package, so RPCTransaction is redefined here as EthRPCTransaction.
type EthRPCTransaction struct {
	BlockHash        *common.Hash    `json:"blockHash"`
	BlockNumber      *hexutil.Big    `json:"blockNumber"`
	From             common.Address  `json:"from"`
	Gas              hexutil.Uint64  `json:"gas"`
	GasPrice         *hexutil.Big    `json:"gasPrice"`
	GasFeeCap        *hexutil.Big    `json:"maxFeePerGas,omitempty"`
	GasTipCap        *hexutil.Big    `json:"maxPriorityFeePerGas,omitempty"`
	Hash             common.Hash     `json:"hash"`
	Input            hexutil.Bytes   `json:"input"`
	Nonce            hexutil.Uint64  `json:"nonce"`
	To               *common.Address `json:"to"`
	TransactionIndex *hexutil.Uint64 `json:"transactionIndex"`
	Value            *hexutil.Big    `json:"value"`
	Type             hexutil.Uint64  `json:"type"`
	Accesses         *AccessList     `json:"accessList,omitempty"`
	ChainID          *hexutil.Big    `json:"chainId,omitempty"`
	V                *hexutil.Big    `json:"v"`
	R                *hexutil.Big    `json:"r"`
	S                *hexutil.Big    `json:"s"`
}

// GetTransactionByBlockNumberAndIndex returns the transaction for the given block number and index.
func (api *EthereumAPI) GetTransactionByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) *EthRPCTransaction {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetTransactionByBlockHashAndIndex returns the transaction for the given block hash and index.
func (api *EthereumAPI) GetTransactionByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) *EthRPCTransaction {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetRawTransactionByBlockNumberAndIndex returns the bytes of the transaction for the given block number and index.
func (api *EthereumAPI) GetRawTransactionByBlockNumberAndIndex(ctx context.Context, blockNr rpc.BlockNumber, index hexutil.Uint) hexutil.Bytes {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetRawTransactionByBlockHashAndIndex returns the bytes of the transaction for the given block hash and index.
func (api *EthereumAPI) GetRawTransactionByBlockHashAndIndex(ctx context.Context, blockHash common.Hash, index hexutil.Uint) hexutil.Bytes {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// GetTransactionCount returns the number of transactions the given address has sent for the given block number.
func (api *EthereumAPI) GetTransactionCount(ctx context.Context, address common.Address, blockNrOrHash rpc.BlockNumberOrHash) (*hexutil.Uint64, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetTransactionByHash returns the transaction for the given hash.
func (api *EthereumAPI) GetTransactionByHash(ctx context.Context, hash common.Hash) (*EthRPCTransaction, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetRawTransactionByHash returns the bytes of the transaction for the given hash.
func (api *EthereumAPI) GetRawTransactionByHash(ctx context.Context, hash common.Hash) (hexutil.Bytes, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// GetTransactionReceipt returns the transaction receipt for the given transaction hash.
func (api *EthereumAPI) GetTransactionReceipt(ctx context.Context, hash common.Hash) (map[string]interface{}, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// EthTransactionArgs represents the arguments to construct a new transaction
// or a message call.
// TransactionArgs in go-ethereum has been renamed to EthTransactionArgs.
// TransactionArgs is defined in go-ethereum's internal package, so TransactionArgs is redefined here as EthTransactionArgs.
type EthTransactionArgs struct {
	From                 *common.Address `json:"from"`
	To                   *common.Address `json:"to"`
	Gas                  *hexutil.Uint64 `json:"gas"`
	GasPrice             *hexutil.Big    `json:"gasPrice"`
	MaxFeePerGas         *hexutil.Big    `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big    `json:"maxPriorityFeePerGas"`
	Value                *hexutil.Big    `json:"value"`
	Nonce                *hexutil.Uint64 `json:"nonce"`

	// We accept "data" and "input" for backwards-compatibility reasons.
	// "input" is the newer name and should be preferred by clients.
	// Issue detail: https://github.com/ethereum/go-ethereum/issues/15628
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input"`

	// Introduced by AccessListTxType transaction.
	AccessList *AccessList  `json:"accessList,omitempty"`
	ChainID    *hexutil.Big `json:"chainId,omitempty"`
}

// from retrieves the transaction sender address.
func (arg *EthTransactionArgs) from() common.Address {
	if arg.From == nil {
		return common.Address{}
	}
	return *arg.From
}

// data retrieves the transaction calldata. Input field is preferred.
func (arg *EthTransactionArgs) data() []byte {
	if arg.Input != nil {
		return *arg.Input
	}
	if arg.Data != nil {
		return *arg.Data
	}
	return nil
}

// setDefaults fills in default values for unspecified tx fields.
func (args *EthTransactionArgs) setDefaults(ctx context.Context, b Backend) error {
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	// After london, default to 1559 uncles gasPrice is set
	head := b.CurrentBlock().Header()
	// TODO-Klaytn: Klaytn is using fixed BaseFee(0) as now but
	// if we apply dynamic BaseFee, we should add calculated BaseFee instead of using params.BaseFee.
	fixedBaseFee := new(big.Int).SetUint64(params.BaseFee)

	// If user specifies both maxPriorityfee and maxFee, then we do not
	// need to consult the chain for defaults. It's definitely a London tx.
	if args.MaxPriorityFeePerGas == nil || args.MaxFeePerGas == nil {
		if b.ChainConfig().IsLondon(head.Number) && args.GasPrice == nil {
			if args.MaxPriorityFeePerGas == nil {
				// TODO-Klaytn: Original logic of Ethereum uses b.SuggestTipCap which suggests TipCap, not a GasPrice.
				// But Klaytn currently uses fixed unit price determined by Governance, so using b.SuggestPrice
				// is fine as now.
				tip, err := b.SuggestPrice(ctx)
				if err != nil {
					return err
				}
				args.MaxPriorityFeePerGas = (*hexutil.Big)(tip)
			}
			if args.MaxFeePerGas == nil {
				// TODO-Klaytn: Calculating formula of gasFeeCap is same with Ethereum except for
				// using fixedBaseFee which means gasFeeCap is always same with args.MaxPriorityFeePerGas as now.
				gasFeeCap := new(big.Int).Add(
					(*big.Int)(args.MaxPriorityFeePerGas),
					new(big.Int).Mul(fixedBaseFee, big.NewInt(2)),
				)
				args.MaxFeePerGas = (*hexutil.Big)(gasFeeCap)
			}
			if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
				return fmt.Errorf("maxFeePerGas (%v) < maxPriorityFeePerGas (%v)", args.MaxFeePerGas, args.MaxPriorityFeePerGas)
			}
		} else {
			if args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil {
				return errors.New("maxFeePerGas or maxPriorityFeePerGas specified but london is not active yet")
			}
			if args.GasPrice == nil {
				// TODO-Klaytn: Original logic of Ethereum uses b.SuggestTipCap which suggests TipCap, not a GasPrice.
				// But Klaytn currently uses fixed unit price determined by Governance, so using b.SuggestPrice
				// is fine as now.
				price, err := b.SuggestPrice(ctx)
				if err != nil {
					return err
				}
				if b.ChainConfig().IsLondon(head.Number) {
					// TODO-Klaytn: Klaytn is using fixed BaseFee(0) as now but
					// if we apply dynamic BaseFee, we should add calculated BaseFee instead of params.BaseFee.
					price.Add(price, new(big.Int).SetUint64(params.BaseFee))
				}
			}
		}
	} else {
		// Both maxPriorityFee and maxFee set by caller. Sanity-check their internal relation
		if args.MaxFeePerGas.ToInt().Cmp(args.MaxPriorityFeePerGas.ToInt()) < 0 {
			return errors.New("maxFeePerGas or maxPriorityFeePerGas specified but london is not active yet")
		}
	}
	if args.Value == nil {
		args.Value = new(hexutil.Big)
	}
	if args.Nonce == nil {
		nonce := b.GetPoolNonce(ctx, args.from())
		args.Nonce = (*hexutil.Uint64)(&nonce)
	}
	if args.Data != nil && args.Input != nil && !bytes.Equal(*args.Data, *args.Input) {
		return errors.New(`both "data" and "input" are set and not equal. Please use "input" to pass transaction call data`)
	}
	if args.To == nil && len(args.data()) == 0 {
		return errors.New(`contract creation without any data provided`)
	}
	// Estimate the gas usage if necessary.
	if args.Gas == nil {
		// These fields are immutable during the estimation, safe to
		// pass the pointer directly.
		data := args.data()
		callArgs := EthTransactionArgs{
			From:                 args.From,
			To:                   args.To,
			GasPrice:             args.GasPrice,
			MaxFeePerGas:         args.MaxFeePerGas,
			MaxPriorityFeePerGas: args.MaxPriorityFeePerGas,
			Value:                args.Value,
			Data:                 (*hexutil.Bytes)(&data),
			AccessList:           args.AccessList,
		}
		pendingBlockNr := rpc.NewBlockNumberOrHashWithNumber(rpc.PendingBlockNumber)
		gasCap := uint64(0)
		if rpcGasCap := b.RPCGasCap(); rpcGasCap != nil {
			gasCap = rpcGasCap.Uint64()
		}
		estimated, err := EthDoEstimateGas(ctx, b, callArgs, pendingBlockNr, gasCap)
		if err != nil {
			return err
		}
		args.Gas = &estimated
		logger.Trace("Estimate gas usage automatically", "gas", args.Gas)
	}
	if args.ChainID == nil {
		id := (*hexutil.Big)(b.ChainConfig().ChainID)
		args.ChainID = id
	}
	return nil
}

func EthDoEstimateGas(ctx context.Context, b Backend, args EthTransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, gasCap uint64) (hexutil.Uint64, error) {
	// Binary search the gas requirement, as it may be higher than the amount used
	var (
		lo  uint64 = params.TxGas - 1
		hi  uint64
		cap uint64
	)
	// Use zero address if sender unspecified.
	if args.From == nil {
		args.From = new(common.Address)
	}
	// Determine the highest gas limit can be used during the estimation.
	if args.Gas != nil && uint64(*args.Gas) >= params.TxGas {
		hi = uint64(*args.Gas)
	} else {
		// Ethereum set hi as gas ceiling of the block but,
		// there is no actual gas limit in Klaytn, so we set it as params.UpperGasLimit.
		hi = params.UpperGasLimit
	}
	// Normalize the max fee per gas the call is willing to spend.
	var feeCap *big.Int
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return 0, errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	} else if args.GasPrice != nil {
		feeCap = args.GasPrice.ToInt()
	} else if args.MaxFeePerGas != nil {
		feeCap = args.MaxFeePerGas.ToInt()
	} else {
		feeCap = common.Big0
	}
	// recap the highest gas limit with account's available balance.
	if feeCap.BitLen() != 0 {
		state, _, err := b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
		if err != nil {
			return 0, err
		}
		balance := state.GetBalance(*args.From) // from can't be nil
		available := new(big.Int).Set(balance)
		if args.Value != nil {
			if args.Value.ToInt().Cmp(available) >= 0 {
				return 0, errors.New("insufficient funds for transfer")
			}
			available.Sub(available, args.Value.ToInt())
		}
		allowance := new(big.Int).Div(available, feeCap)

		// If the allowance is larger than maximum uint64, skip checking
		if allowance.IsUint64() && hi > allowance.Uint64() {
			transfer := args.Value
			if transfer == nil {
				transfer = new(hexutil.Big)
			}
			logger.Warn("Gas estimation capped by limited funds", "original", hi, "balance", balance,
				"sent", transfer.ToInt(), "maxFeePerGas", feeCap, "fundable", allowance)
			hi = allowance.Uint64()
		}
	}
	// Recap the highest gas allowance with specified gascap.
	if gasCap != 0 && hi > gasCap {
		logger.Warn("Caller gas above allowance, capping", "requested", hi, "cap", gasCap)
		hi = gasCap
	}
	cap = hi

	// Create a helper to check if a gas allowance results in an executable transaction
	executable := func(gas uint64) (bool, error) {
		args.Gas = (*hexutil.Uint64)(&gas)
		_, _, err := EthDoCall(ctx, b, args, rpc.NewBlockNumberOrHashWithNumber(rpc.LatestBlockNumber), nil, 0, gasCap)
		if err != nil {
			return false, err
		}
		return true, nil
	}
	// Execute the binary search and hone in on an executable gas limit
	for lo+1 < hi {
		mid := (hi + lo) / 2
		isExecutable, _ := executable(mid)

		if !isExecutable {
			lo = mid
		} else {
			hi = mid
		}
	}
	// Reject the transaction as invalid if it still fails at the highest allowance
	if hi == cap {
		isExecutable, err := executable(hi)
		if err != nil {
			return 0, err
		}
		if !isExecutable {
			return 0, fmt.Errorf("gas required exceeds allowance or always failing transaction")
		}
	}
	return hexutil.Uint64(hi), nil
}

// ToMessage change EthTransactionArgs to types.Transaction in Klaytn.
func (args *EthTransactionArgs) ToMessage(globalGasCap uint64, baseFee *big.Int, intrinsicGas uint64) (*types.Transaction, error) {
	// Reject invalid combinations of pre- and post-1559 fee styles
	if args.GasPrice != nil && (args.MaxFeePerGas != nil || args.MaxPriorityFeePerGas != nil) {
		return nil, errors.New("both gasPrice and (maxFeePerGas or maxPriorityFeePerGas) specified")
	}
	// Set sender address or use zero address if none specified.
	addr := args.from()

	// Set default gas & gas price if none were set
	gas := globalGasCap
	if gas == 0 {
		gas = uint64(math.MaxUint64 / 2)
	}
	if args.Gas != nil {
		gas = uint64(*args.Gas)
	}
	if globalGasCap != 0 && globalGasCap < gas {
		logger.Warn("Caller gas above allowance, capping", "requested", gas, "cap", globalGasCap)
		gas = globalGasCap
	}
	var (
		gasPrice  *big.Int
		gasFeeCap *big.Int
		gasTipCap *big.Int
	)
	if baseFee == nil {
		// If there's no basefee, then it must be a non-1559 execution
		gasPrice = new(big.Int)
		if args.GasPrice != nil {
			gasPrice = args.GasPrice.ToInt()
		}
		gasFeeCap, gasTipCap = gasPrice, gasPrice
	} else {
		// A basefee is provided, necessitating 1559-type execution
		if args.GasPrice != nil {
			// User specified the legacy gas field, convert to 1559 gas typing
			gasPrice = args.GasPrice.ToInt()
			gasFeeCap, gasTipCap = gasPrice, gasPrice
		} else {
			// User specified 1559 gas fields (or none), use those
			gasFeeCap = new(big.Int)
			if args.MaxFeePerGas != nil {
				gasFeeCap = args.MaxFeePerGas.ToInt()
			}
			gasTipCap = new(big.Int)
			if args.MaxPriorityFeePerGas != nil {
				gasTipCap = args.MaxPriorityFeePerGas.ToInt()
			}
			// Backfill the legacy gasPrice for EVM execution, unless we're all zeros
			gasPrice = new(big.Int)
			if gasFeeCap.BitLen() > 0 || gasTipCap.BitLen() > 0 {
				gasPrice = math.BigMin(new(big.Int).Add(gasTipCap, baseFee), gasFeeCap)
			}
		}
	}
	value := new(big.Int)
	if args.Value != nil {
		value = args.Value.ToInt()
	}
	data := args.data()

	// TODO-Klaytn: Klaytn does not support accessList yet.
	// var accessList AccessList
	// if args.AccessList != nil {
	//	 accessList = *args.AccessList
	// }
	return types.NewMessage(addr, args.To, 0, value, gas, gasPrice, data, false, intrinsicGas), nil
}

// SendTransaction creates a transaction for the given argument, sign it and submit it to the
// transaction pool.
func (api *EthereumAPI) SendTransaction(ctx context.Context, args EthTransactionArgs) (common.Hash, error) {
	// TODO-Klaytn: Not implemented yet.
	return common.HexToHash("0x"), nil
}

// EthSignTransactionResult represents a RLP encoded signed transaction.
// SignTransactionResult in go-ethereum has been renamed to EthSignTransactionResult.
// SignTransactionResult is defined in go-ethereum's internal package, so SignTransactionResult is redefined here as EthSignTransactionResult.
type EthSignTransactionResult struct {
	Raw hexutil.Bytes `json:"raw"`
	Tx  *Transaction  `json:"tx"`
}

// Transaction is an Ethereum transaction.
type Transaction struct {
	inner TxData    // Consensus contents of a transaction
	time  time.Time // Time first seen locally (spam avoidance)

	// caches
	hash atomic.Value
	size atomic.Value
	from atomic.Value
}

// TxData is the underlying data of a transaction.
//
// This is implemented by DynamicFeeTx, LegacyTx and AccessListTx.
type TxData interface {
	txType() byte // returns the type ID
	copy() TxData // creates a deep copy and initializes all fields

	chainID() *big.Int
	accessList() AccessList
	data() []byte
	gas() uint64
	gasPrice() *big.Int
	gasTipCap() *big.Int
	gasFeeCap() *big.Int
	value() *big.Int
	nonce() uint64
	to() *common.Address

	rawSignatureValues() (v, r, s *big.Int)
	setSignatureValues(chainID, v, r, s *big.Int)
}

// FillTransaction fills the defaults (nonce, gas, gasPrice or 1559 fields)
// on a given unsigned transaction, and returns it to the caller for further
// processing (signing + broadcast).
func (api *EthereumAPI) FillTransaction(ctx context.Context, args EthTransactionArgs) (*EthSignTransactionResult, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// SendRawTransaction will add the signed transaction to the transaction pool.
// The sender is responsible for signing the transaction and using the correct nonce.
func (api *EthereumAPI) SendRawTransaction(ctx context.Context, input hexutil.Bytes) (common.Hash, error) {
	// TODO-Klaytn: Not implemented yet.
	return common.HexToHash("0x"), nil
}

// Sign calculates an ECDSA signature for:
// keccack256("\x19Ethereum Signed Message:\n" + len(message) + message).
//
// Note, the produced signature conforms to the secp256k1 curve R, S and V values,
// where the V value will be 27 or 28 for legacy reasons.
//
// The account associated with addr must be unlocked.
//
// https://github.com/ethereum/wiki/wiki/JSON-RPC#eth_sign
func (api *EthereumAPI) Sign(addr common.Address, data hexutil.Bytes) (hexutil.Bytes, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// SignTransaction will sign the given transaction with the from account.
// The node needs to have the private key of the account corresponding with
// the given from address and it needs to be unlocked.
func (api *EthereumAPI) SignTransaction(ctx context.Context, args EthTransactionArgs) (*EthSignTransactionResult, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// PendingTransactions returns the transactions that are in the transaction pool
// and have a from address that is one of the accounts this node manages.
func (api *EthereumAPI) PendingTransactions() ([]*EthRPCTransaction, error) {
	// TODO-Klaytn: Not implemented yet.
	return nil, nil
}

// Resend accepts an existing transaction and a new gas price and limit. It will remove
// the given transaction from the pool and reinsert it with the new gas price and limit.
func (api *EthereumAPI) Resend(ctx context.Context, sendArgs EthTransactionArgs, gasPrice *hexutil.Big, gasLimit *hexutil.Uint64) (common.Hash, error) {
	// TODO-Klaytn: Not implemented yet.
	return common.HexToHash("0x"), nil
}

// Accounts returns the collection of accounts this node manages.
func (api *EthereumAPI) Accounts() []common.Address {
	// TODO-Klaytn: Not implemented yet.
	return nil
}

// rpcMarshalHeader marshal block header as Ethereum compatible format.
// It returns error when fetching Author which is block proposer is failed.
func (api *EthereumAPI) rpcMarshalHeader(head *types.Header) (map[string]interface{}, error) {
	proposer, err := api.publicKlayAPI.b.Engine().Author(head)
	if err != nil {
		// miner is the field Klaytn should provide the correct value. It's not the field dummy value is allowed.
		logger.Error("Failed to fetch author during marshaling header", "err", err.Error())
		return nil, err
	}
	result := map[string]interface{}{
		"number":          (*hexutil.Big)(head.Number),
		"hash":            head.Hash(),
		"parentHash":      head.ParentHash,
		"nonce":           BlockNonce{},  // There is no block nonce concept in Klaytn, so it must be empty.
		"mixHash":         common.Hash{}, // Klaytn does not use mixHash, so it must be empty.
		"sha3Uncles":      common.HexToHash(EmptySha3Uncles),
		"logsBloom":       head.Bloom,
		"stateRoot":       head.Root,
		"miner":           proposer,
		"difficulty":      (*hexutil.Big)(head.BlockScore),
		"totalDifficulty": (*hexutil.Big)(api.publicKlayAPI.b.GetTd(head.Hash())),
		// extraData always return empty Bytes because actual value of extraData in Klaytn header cannot be used as meaningful way because
		// we cannot provide original header of Klaytn and this field is used as consensus info which is encoded value of validators addresses, validators signatures, and proposer signature in Klaytn.
		"extraData":        hexutil.Bytes{},
		"size":             hexutil.Uint64(head.Size()),
		"gasLimit":         hexutil.Uint64(DummyGasLimit),
		"gasUsed":          hexutil.Uint64(head.GasUsed),
		"timestamp":        hexutil.Big(*head.Time),
		"transactionsRoot": head.TxHash,
		"receiptsRoot":     head.ReceiptHash,
		"baseFeePerGas":    (*hexutil.Big)(new(big.Int).SetUint64(params.BaseFee)),
	}

	return result, nil
}

func EthDoCall(ctx context.Context, b Backend, args EthTransactionArgs, blockNrOrHash rpc.BlockNumberOrHash, overrides *EthStateOverride, timeout time.Duration, globalGasCap uint64) ([]byte, uint64, error) {
	defer func(start time.Time) { logger.Debug("Executing EVM call finished", "runtime", time.Since(start)) }(time.Now())

	st, header, err := b.StateAndHeaderByNumberOrHash(ctx, blockNrOrHash)
	if st == nil || err != nil {
		return nil, 0, err
	}
	if err := overrides.Apply(st); err != nil {
		return nil, 0, err
	}

	// Setup context so it may be cancelled the call has completed
	// or, in case of unmetered gas, setup a context with a timeout.
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	// Make sure the context is cancelled when the call has completed
	// this makes sure resources are cleaned up.
	defer cancel()

	// TODO-Klaytn: Klaytn is using fixed baseFee as now.
	fixedBaseFee := new(big.Int).SetUint64(params.BaseFee)
	intrinsicGas, err := types.IntrinsicGas(args.data(), args.To == nil, b.ChainConfig().Rules(header.Number))
	if err != nil {
		return nil, 0, err
	}
	msg, err := args.ToMessage(globalGasCap, fixedBaseFee, intrinsicGas)
	if err != nil {
		return nil, 0, err
	}
	evm, vmError, err := b.GetEVM(ctx, msg, st, header, vm.Config{})
	if err != nil {
		return nil, 0, err
	}
	// Wait for the context to be done and cancel the evm. Even if the
	// EVM has finished, cancelling may be done (repeatedly)
	go func() {
		<-ctx.Done()
		evm.Cancel(vm.CancelByCtxDone)
	}()

	// Execute the message.
	res, gas, kerr := blockchain.ApplyMessage(evm, msg)
	err = kerr.ErrTxInvalid
	if err := vmError(); err != nil {
		return nil, 0, err
	}
	// If the timer caused an abort, return an appropriate error message
	if evm.Cancelled() {
		return nil, 0, fmt.Errorf("execution aborted (timeout = %v)", timeout)
	}

	if err == nil {
		err = blockchain.GetVMerrFromReceiptStatus(kerr.Status)
	}
	if err != nil {
		return nil, 0, fmt.Errorf("err: %w (supplied gas %d)", err, msg.Gas())
	}
	return res, gas, nil
}

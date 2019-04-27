// Copyright 2018 The Fractal Team Authors
// This file is part of the fractal project.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package ftservice

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common/math"
	"github.com/fractalplatform/fractal/accountmanager"
	"github.com/fractalplatform/fractal/common"
	"github.com/fractalplatform/fractal/consensus"
	"github.com/fractalplatform/fractal/feemanager"
	"github.com/fractalplatform/fractal/ftservice/gasprice"
	"github.com/fractalplatform/fractal/p2p/enode"
	"github.com/fractalplatform/fractal/params"
	"github.com/fractalplatform/fractal/processor"
	"github.com/fractalplatform/fractal/processor/vm"
	"github.com/fractalplatform/fractal/rawdb"
	"github.com/fractalplatform/fractal/rpc"
	"github.com/fractalplatform/fractal/state"
	"github.com/fractalplatform/fractal/types"
	"github.com/fractalplatform/fractal/utils/fdb"
)

// APIBackend implements ftserviceapi.Backend for full nodes
type APIBackend struct {
	ftservice *FtService
	gpo       *gasprice.Oracle
}

// ChainConfig returns the active chain configuration.
func (b *APIBackend) ChainConfig() *params.ChainConfig {
	return b.ftservice.chainConfig
}
func (b *APIBackend) SuggestPrice(ctx context.Context) (*big.Int, error) {
	return b.gpo.SuggestPrice(ctx)
}

func (b *APIBackend) GetLogs(ctx context.Context, hash common.Hash) ([][]*types.Log, error) {
	number := rawdb.ReadHeaderNumber(b.ftservice.chainDb, hash)
	if number == nil {
		return nil, nil
	}
	receipts := rawdb.ReadReceipts(b.ftservice.chainDb, hash, *number)
	if receipts == nil {
		return nil, nil
	}
	logs := make([][]*types.Log, len(receipts))
	for i, receipt := range receipts {
		logs[i] = receipt.Logs
	}
	return logs, nil
}

func (b *APIBackend) SendTx(ctx context.Context, signedTx *types.Transaction) error {
	return b.ftservice.txPool.AddLocal(signedTx)
}

func (b *APIBackend) GetPoolTransactions() ([]*types.Transaction, error) {
	pending, err := b.ftservice.txPool.Pending()
	if err != nil {
		return nil, err
	}
	var txs []*types.Transaction
	for _, batch := range pending {
		txs = append(txs, batch...)
	}
	return txs, nil
}

func (b *APIBackend) GetPoolTransaction(hash common.Hash) *types.Transaction {
	return b.ftservice.txPool.Get(hash)
}

func (b *APIBackend) Stats() (pending int, queued int) {
	return b.ftservice.txPool.Stats()
}

func (b *APIBackend) TxPoolContent() (map[common.Name][]*types.Transaction, map[common.Name][]*types.Transaction) {
	return b.ftservice.TxPool().Content()
}

func (b *APIBackend) ChainDb() fdb.Database {
	return b.ftservice.chainDb
}

func (b *APIBackend) CurrentBlock() *types.Block {
	return b.ftservice.blockchain.CurrentBlock()
}

func (b *APIBackend) GetBlock(ctx context.Context, hash common.Hash) (*types.Block, error) {
	return b.ftservice.blockchain.GetBlockByHash(hash), nil
}

func (b *APIBackend) GetReceipts(ctx context.Context, hash common.Hash) ([]*types.Receipt, error) {
	if number := rawdb.ReadHeaderNumber(b.ftservice.chainDb, hash); number != nil {
		return rawdb.ReadReceipts(b.ftservice.chainDb, hash, *number), nil
	}
	return nil, nil
}

func (b *APIBackend) GetDetailTxsLog(ctx context.Context, hash common.Hash) ([]*types.DetailTx, error) {
	if number := rawdb.ReadHeaderNumber(b.ftservice.chainDb, hash); number != nil {
		return rawdb.ReadDetailTxs(b.ftservice.chainDb, hash, *number), nil
	}
	return nil, nil
}

func (b *APIBackend) GetBlockDetailLog(ctx context.Context, blockNr rpc.BlockNumber) *types.BlockAndResult {
	hash := rawdb.ReadCanonicalHash(b.ftservice.chainDb, uint64(blockNr))
	if hash == (common.Hash{}) {
		return nil
	}
	receipts := rawdb.ReadReceipts(b.ftservice.chainDb, hash, uint64(blockNr))
	txDetails := rawdb.ReadDetailTxs(b.ftservice.chainDb, hash, uint64(blockNr))
	return &types.BlockAndResult{
		Receipts:  receipts,
		DetailTxs: txDetails,
	}
}

func (b *APIBackend) GetTxsByFilter(ctx context.Context, filterFn func(common.Name) bool, blockNr rpc.BlockNumber, lookbackNum uint64) []common.Hash {
	lastnum := uint64(blockNr) - lookbackNum

	txHashs := make([]common.Hash, 0)

	for ublocknum := uint64(blockNr); ublocknum > lastnum; ublocknum-- {

		hash := rawdb.ReadCanonicalHash(b.ftservice.chainDb, ublocknum)
		if hash == (common.Hash{}) {
			continue
		}

		blockBody := rawdb.ReadBody(b.ftservice.chainDb, hash, ublocknum)
		if blockBody == nil {
			continue
		}
		batch_txs := blockBody.Transactions

		for _, tx := range batch_txs {
			for _, act := range tx.GetActions() {
				if filterFn(act.Sender()) || filterFn(act.Recipient()) {
					txHashs = append(txHashs, tx.Hash())
					break
				}
			}
		}
	}

	return txHashs
}

func (b *APIBackend) GetDetailTxByFilter(ctx context.Context, filterFn func(common.Name) bool, blockNr rpc.BlockNumber, lookbackNum uint64) []*types.DetailTx {
	lastnum := uint64(blockNr) - lookbackNum

	txdetails := make([]*types.DetailTx, 0)

	for ublocknum := uint64(blockNr); ublocknum > lastnum; ublocknum-- {

		hash := rawdb.ReadCanonicalHash(b.ftservice.chainDb, ublocknum)
		if hash == (common.Hash{}) {
			continue
		}

		batch_txdetails := rawdb.ReadDetailTxs(b.ftservice.chainDb, hash, ublocknum)
		for _, txd := range batch_txdetails {

			new_intxs := make([]*types.DetailAction, 0)
			for _, intx := range txd.Actions {
				new_inactions := make([]*types.InternalAction, 0)
				for _, inlog := range intx.InternalActions {
					if filterFn(inlog.Action.From) || filterFn(inlog.Action.To) {
						new_inactions = append(new_inactions, inlog)
					}
				}
				if len(new_inactions) > 0 {
					new_intxs = append(new_intxs, &types.DetailAction{InternalActions: new_inactions})
				}
			}

			if len(new_intxs) > 0 {
				txdetails = append(txdetails, &types.DetailTx{TxHash: txd.TxHash, Actions: new_intxs})
			}
		}
	}

	return txdetails
}

func (b *APIBackend) GetBadBlocks(ctx context.Context) ([]*types.Block, error) {
	return b.ftservice.blockchain.BadBlocks(), nil
}

func (b *APIBackend) GetTd(blockHash common.Hash) *big.Int {
	return b.ftservice.blockchain.GetTdByHash(blockHash)
}

func (b *APIBackend) HeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.Header, error) {

	// Pending block is only known by the miner
	if blockNr == rpc.PendingBlockNumber {
		block, _ := b.ftservice.miner.Pending()
		return block.Header(), nil
	}

	// Otherwise resolve and return the block
	if blockNr == rpc.LatestBlockNumber {
		return b.ftservice.blockchain.CurrentBlock().Header(), nil
	}

	return b.ftservice.blockchain.GetHeaderByNumber(uint64(blockNr)), nil
}

func (b *APIBackend) BlockByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*types.Block, error) {
	// Pending block is only known by the miner
	if blockNr == rpc.PendingBlockNumber {
		block, _ := b.ftservice.miner.Pending()
		return block, nil
	}

	// Otherwise resolve and return the block
	if blockNr == rpc.LatestBlockNumber {
		return b.ftservice.blockchain.CurrentBlock(), nil
	}
	return b.ftservice.blockchain.GetBlockByNumber(uint64(blockNr)), nil
}

//
func (b *APIBackend) StateAndHeaderByNumber(ctx context.Context, blockNr rpc.BlockNumber) (*state.StateDB, *types.Header, error) {
	// Pending state is only known by the miner
	if blockNr == rpc.PendingBlockNumber {
		block, state := b.ftservice.miner.Pending()
		return state, block.Header(), nil
	}
	// Otherwise resolve the block number and return its state
	header, err := b.HeaderByNumber(ctx, blockNr)
	if header == nil || err != nil {
		return nil, nil, err
	}
	stateDb, err := b.ftservice.blockchain.StateAt(b.ftservice.blockchain.CurrentBlock().Root())
	return stateDb, header, err
}

func (b *APIBackend) GetEVM(ctx context.Context, account *accountmanager.AccountManager, state *state.StateDB, from common.Name, assetID uint64, gasPrice *big.Int, header *types.Header, vmCfg vm.Config) (*vm.EVM, func() error, error) {
	account.AddAccountBalanceByID(from, assetID, math.MaxBig256)
	vmError := func() error { return nil }

	evmcontext := &processor.EvmContext{
		ChainContext:  b.ftservice.BlockChain(),
		EgnineContext: b.ftservice.Engine(),
	}

	context := processor.NewEVMContext(from, assetID, gasPrice, header, evmcontext, nil)
	return vm.NewEVM(context, account, state, b.ChainConfig(), vmCfg), vmError, nil
}

func (b *APIBackend) SetGasPrice(gasPrice *big.Int) bool {
	b.ftservice.SetGasPrice(gasPrice)
	return true
}

func (b *APIBackend) GetAccountManager() (*accountmanager.AccountManager, error) {
	sdb, err := b.ftservice.blockchain.State()
	if err != nil {
		return nil, err
	}
	acctm, err := accountmanager.NewAccountManager(sdb)
	if err != nil {
		return nil, err
	}
	return acctm, nil
}

//GetFeeManager get fee manager
func (b *APIBackend) GetFeeManager() (*feemanager.FeeManager, error) {
	sdb, err := b.ftservice.blockchain.State()
	if err != nil {
		return nil, err
	}
	acctm, err := accountmanager.NewAccountManager(sdb)
	if err != nil {
		return nil, err
	}

	fm := feemanager.NewFeeManager(sdb, acctm)
	return fm, nil
}

// AddPeer add a P2P peer
func (b *APIBackend) AddPeer(url string) error {
	node, err := enode.ParseV4(url)
	if err == nil {
		b.ftservice.p2pServer.AddPeer(node)
	}
	return err
}

// RemovePeer remove a P2P peer
func (b *APIBackend) RemovePeer(url string) error {
	node, err := enode.ParseV4(url)
	if err == nil {
		b.ftservice.p2pServer.RemovePeer(node)
	}
	return err
}

// AddTrustedPeer allows a remote node to always connect, even if slots are full
func (b *APIBackend) AddTrustedPeer(url string) error {
	node, err := enode.ParseV4(url)
	if err == nil {
		b.ftservice.p2pServer.AddTrustedPeer(node)
	}
	return err
}

// RemoveTrustedPeer removes a remote node from the trusted peer set, but it
// does not disconnect it automatically.
func (b *APIBackend) RemoveTrustedPeer(url string) error {
	node, err := enode.ParseV4(url)
	if err == nil {
		b.ftservice.p2pServer.RemoveTrustedPeer(node)
	}
	return err
}

// PeerCount returns the number of connected peers.
func (b *APIBackend) PeerCount() int {
	return b.ftservice.p2pServer.PeerCount()
}

// Peers returns all connected peers.
func (b *APIBackend) Peers() []string {
	ps := b.ftservice.p2pServer.Peers()
	peers := make([]string, len(ps))
	for i, peer := range ps {
		peers[i] = peer.Node().String()
	}
	return peers
}

// BadNodesCount returns the number of bad nodes.
func (b *APIBackend) BadNodesCount() int {
	return b.ftservice.p2pServer.BadNodesCount()
}

// BadNodes returns all bad nodes.
func (b *APIBackend) BadNodes() []string {
	nodes := b.ftservice.p2pServer.BadNodes()
	ns := make([]string, len(nodes))
	for i, node := range nodes {
		ns[i] = node.String()
	}
	return ns
}

// AddBadNode add a bad Node and would cause the node disconnected
func (b *APIBackend) AddBadNode(url string) error {
	node, err := enode.ParseV4(url)
	if err == nil {
		b.ftservice.p2pServer.AddBadNode(node)
	}
	return err
}

// SelfNode returns the local node's endpoint information.
func (b *APIBackend) SelfNode() string {
	return b.ftservice.p2pServer.Self().String()
}

// APIs returns apis
func (b *APIBackend) Engine() consensus.IEngine {
	return b.ftservice.engine
}

//SetStatePruning set state pruning
func (b *APIBackend) SetStatePruning(enable bool) (bool, uint64) {
	return b.ftservice.blockchain.StatePruning(enable)
}

// APIs returns apis
func (b *APIBackend) APIs() []rpc.API {
	return b.ftservice.miner.APIs(b.ftservice.blockchain)
}

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

package blockchain

import (
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
	"github.com/fractalplatform/fractal/common"
	"github.com/fractalplatform/fractal/common/prque"
	"github.com/fractalplatform/fractal/event"
	"github.com/fractalplatform/fractal/params"
	"github.com/fractalplatform/fractal/processor"
	"github.com/fractalplatform/fractal/processor/vm"
	"github.com/fractalplatform/fractal/rawdb"
	"github.com/fractalplatform/fractal/state"
	"github.com/fractalplatform/fractal/types"
	"github.com/fractalplatform/fractal/utils/fdb"
	"github.com/fractalplatform/fractal/utils/rlp"
	lru "github.com/hashicorp/golang-lru"
)

const (
	bodyCacheLimit      = 256
	blockCacheLimit     = 256
	headerCacheLimit    = 512
	tdCacheLimit        = 1024
	numberCacheLimit    = 2048
	maxFutureBlocks     = 256
	maxTimeFutureBlocks = 30
	badBlockLimit       = 10

	//BlockChainVersion ensures that an incompatible database forces a resync from scratch.
	BlockChainVersion = 0
)

type CacheConfig struct {
}

// BlockChain represents the canonical chain given a database with a genesis
// block. The Blockchain manages chain imports, reverts, chain reorganisations.
type BlockChain struct {
	chainConfig *params.ChainConfig // Chain & network configuration

	statePruning      bool
	statePruningClean bool
	snapshotInterval  uint64
	triesInMemory     uint64
	preSnapshotTime   uint64
	dereferenceNumber uint64
	triegc            *prque.Prque

	vmConfig           vm.Config    // vm configuration
	genesisBlock       *types.Block // genesis block
	db                 fdb.Database // Low level persistent database to store final content in
	mu                 sync.RWMutex // global mutex for locking chain operations
	chainmu            sync.RWMutex // blockchain insertion lock
	procmu             sync.RWMutex // block processor lock
	currentBlock       atomic.Value // Current head of the block chain
	irreversibleNumber atomic.Value // irreversible Number of the block chain

	stateCache state.Database // State database to reuse between imports (contains state cache)

	running       int32               // running must be called atomically
	procInterrupt int32               // procInterrupt must be atomically called, interrupt signaler for block processing
	wg            sync.WaitGroup      // chain processing wait group for shutting down
	senderCacher  TxSenderCacher      // senderCacher is a concurrent tranaction sender recoverer sender cacher.
	fcontroller   *ForkController     // fcontroller
	processor     processor.Processor // block processor interface
	validator     processor.Validator // block and state validator interface
	station       *BlockchainStation  // p2p station

	headerCache  *lru.Cache    // Cache for the most recent block headers
	tdCache      *lru.Cache    // Cache for the most recent block total difficulties
	numberCache  *lru.Cache    // Cache for the most recent block numbers
	bodyCache    *lru.Cache    // Cache for the most recent block bodies
	bodyRLPCache *lru.Cache    // Cache for the most recent block bodies in RLP encoded format
	blockCache   *lru.Cache    // Cache for the most recent entire blocks
	futureBlocks *lru.Cache    // future blocks are blocks added for later processing
	badBlocks    *lru.Cache    // Bad block cache
	quit         chan struct{} // blockchain quit channel
}

// NewBlockChain returns a fully initialised block chain using information　available in the database.
func NewBlockChain(db fdb.Database, statePruning bool, vmConfig vm.Config, chainConfig *params.ChainConfig, senderCacher TxSenderCacher) (*BlockChain, error) {
	bodyCache, _ := lru.New(bodyCacheLimit)
	bodyRLPCache, _ := lru.New(bodyCacheLimit)
	headerCache, _ := lru.New(headerCacheLimit)
	tdCache, _ := lru.New(tdCacheLimit)
	numberCache, _ := lru.New(numberCacheLimit)
	blockCache, _ := lru.New(blockCacheLimit)
	futureBlocks, _ := lru.New(maxFutureBlocks)
	badBlocks, _ := lru.New(badBlockLimit)

	bc := &BlockChain{
		chainConfig:       chainConfig,
		statePruning:      statePruning,
		statePruningClean: true,
		snapshotInterval:  chainConfig.SnapshotInterval * uint64(time.Millisecond),
		triesInMemory:     ((chainConfig.DposCfg.BlockFrequency * chainConfig.DposCfg.CandidateScheduleSize) * 2) + 2,
		preSnapshotTime:   0,
		dereferenceNumber: 0,
		triegc:            prque.New(nil),
		vmConfig:          vmConfig,
		db:                db,
		stateCache:        state.NewDatabase(db),
		quit:              make(chan struct{}),
		bodyCache:         bodyCache,
		headerCache:       headerCache,
		tdCache:           tdCache,
		numberCache:       numberCache,
		bodyRLPCache:      bodyRLPCache,
		blockCache:        blockCache,
		futureBlocks:      futureBlocks,
		badBlocks:         badBlocks,
		senderCacher:      senderCacher,
		fcontroller: NewForkController(&ForkConfig{
			ForkBlockNum:   chainConfig.ForkedCfg.ForkBlockNum,
			Forkpercentage: chainConfig.ForkedCfg.Forkpercentage,
		}, chainConfig),
	}

	bc.genesisBlock = bc.GetBlockByNumber(0)
	if bc.genesisBlock == nil {
		return nil, ErrNoGenesis
	}

	if err := bc.loadLastBlock(); err != nil {
		return nil, err
	}
	bc.station = newBlcokchainStation(bc, 0)
	go bc.update()
	return bc, nil
}

// loadLastBlock loads the last known chain from the database.
func (bc *BlockChain) loadLastBlock() error {
	// Restore the last known head block
	head := rawdb.ReadHeadBlockHash(bc.db)
	if head == (common.Hash{}) {
		log.Warn("Empty database, resetting chain")
		return bc.Reset()
	}

	// Make sure the entire head block is available
	currentBlock := bc.GetBlockByHash(head)
	if currentBlock == nil {
		log.Warn("Head block missing, resetting chain", "hash", head)
		return bc.Reset()
	}

	// Make sure the state associated with the block is available
	if _, err := state.New(currentBlock.Root(), bc.stateCache); err != nil {
		// Dangling block without a state associated, init from scratch
		log.Warn("Head state missing, repairing chain", "number", currentBlock.Number(), "hash", currentBlock.Hash())
		if err := bc.repair(&currentBlock); err != nil {
			return err
		}
	}

	// Load SnapshotTime
	bc.preSnapshotTime = (currentBlock.Time().Uint64() / bc.snapshotInterval) * bc.snapshotInterval

	// Everything seems to be fine, set as the head block
	bc.currentBlock.Store(currentBlock)

	// Restore the last known head header
	currentHeader := currentBlock.Header()

	rawdb.WriteHeadHeaderHash(bc.db, currentHeader.Hash())
	blockTd := bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())

	inum := rawdb.ReadIrreversibleNumber(bc.db)
	bc.irreversibleNumber.Store(inum)

	log.Info("Loaded most recent local full block", "number", currentBlock.Number(), "hash", currentBlock.Hash(), "td", blockTd, "irreversible number", inum)
	return nil
}

// Reset purges the entire blockchain, restoring it to its genesis state.
func (bc *BlockChain) Reset() error {
	return bc.ResetWithGenesisBlock(bc.genesisBlock)
}

func (bc *BlockChain) repair(head **types.Block) error {
	for {
		// Abort if we've rewound to a head block that does have associated state
		if _, err := state.New((*head).Root(), bc.stateCache); err == nil {
			log.Info("Rewound blockchain to past state", "number", (*head).Number(), "hash", (*head).Hash())
			return nil
		}
		// Otherwise rewind one block and recheck state availability there
		block := bc.GetBlock((*head).ParentHash(), (*head).NumberU64()-1)
		if block == nil {
			return fmt.Errorf("missing block %d [%x]", (*head).NumberU64()-1, (*head).ParentHash())
		}
		*head = block
	}
}

// ResetWithGenesisBlock purges the entire blockchain, restoring it to the specified genesis state.
func (bc *BlockChain) ResetWithGenesisBlock(genesis *types.Block) error {
	// Dump the entire block chain and purge the caches
	if err := bc.SetHead(0); err != nil {
		return err
	}
	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Prepare the genesis block and reinitialise the chain
	rawdb.WriteBlock(bc.db, genesis)
	bc.genesisBlock = genesis
	batch := bc.db.NewBatch()
	bc.insert(batch, bc.genesisBlock)
	if err := batch.Write(); err != nil {
		return err
	}

	rawdb.WriteIrreversibleNumber(bc.db, bc.genesisBlock.NumberU64())

	bc.currentBlock.Store(bc.genesisBlock)
	bc.irreversibleNumber.Store(bc.genesisBlock.NumberU64())

	return nil
}

// SetHead rewinds the local chain to a new head.
func (bc *BlockChain) SetHead(head uint64) error {
	log.Warn("Rewinding blockchain", "target", head)

	bc.mu.Lock()
	defer bc.mu.Unlock()

	// Clear out any stale content from the caches
	bc.headerCache.Purge()
	bc.numberCache.Purge()
	bc.tdCache.Purge()
	bc.bodyCache.Purge()
	bc.bodyRLPCache.Purge()
	bc.blockCache.Purge()
	bc.futureBlocks.Purge()

	// If either blocks reached nil, reset to the genesis state
	if currentBlock := bc.CurrentBlock(); currentBlock == nil {
		bc.currentBlock.Store(bc.genesisBlock)
	}

	currentBlock := bc.CurrentBlock()

	rawdb.WriteHeadBlockHash(bc.db, currentBlock.Hash())
	return bc.loadLastBlock()
}

// GasLimit returns the gas limit of the current HEAD block.
func (bc *BlockChain) GasLimit() uint64 {
	return bc.CurrentBlock().GasLimit()
}

// CurrentBlock retrieves the current head block of the canonical chain.
func (bc *BlockChain) CurrentBlock() *types.Block {
	return bc.currentBlock.Load().(*types.Block)
}

// IrreversibleNumber retrieves the irreversible block number of the canonical chain.
func (bc *BlockChain) IrreversibleNumber() uint64 {
	return bc.irreversibleNumber.Load().(uint64)
}

// SetProcessor sets the processor required for making state modifications.
func (bc *BlockChain) SetProcessor(processor processor.Processor) {
	bc.procmu.Lock()
	defer bc.procmu.Unlock()
	bc.processor = processor
}

// SetValidator sets the processor validator.
func (bc *BlockChain) SetValidator(validator processor.Validator) {
	bc.procmu.RLock()
	defer bc.procmu.RUnlock()
	bc.validator = validator
}

// Validator returns the current validator.
func (bc *BlockChain) Validator() processor.Validator {
	bc.procmu.RLock()
	defer bc.procmu.RUnlock()
	return bc.validator
}

// Processor returns the current processor.
func (bc *BlockChain) Processor() processor.Processor {
	bc.procmu.RLock()
	defer bc.procmu.RUnlock()
	return bc.processor
}

// State returns a new mutable state based on the current HEAD block.
func (bc *BlockChain) State() (*state.StateDB, error) {
	return bc.StateAt(bc.CurrentBlock().Root())
}

// GetForkID returns the last current fork ID.
func (bc *BlockChain) GetForkID(statedb *state.StateDB) (uint64, uint64, error) {
	return bc.fcontroller.currentForkID(statedb)
}

// CheckForkID Checks the validity of forkID
func (bc *BlockChain) CheckForkID(header *types.Header) error {
	parentHeader := bc.GetHeader(header.ParentHash, uint64(header.Number.Int64()-1))
	state, err := bc.StateAt(parentHeader.Root)
	if err != nil {
		return err
	}
	return bc.fcontroller.checkForkID(header, state)
}

// FillForkID fills the current and next forkID
func (bc *BlockChain) FillForkID(header *types.Header, statedb *state.StateDB) error {
	return bc.fcontroller.fillForkID(header, statedb)
}

// StateAt returns a new mutable state based on a particular point in time.
func (bc *BlockChain) StateAt(hash common.Hash) (*state.StateDB, error) {
	return state.New(hash, bc.stateCache)
}

// insert injects a new head block into the current block chain.
func (bc *BlockChain) insert(batch fdb.Batch, block *types.Block) {
	rawdb.WriteCanonicalHash(batch, block.Hash(), block.NumberU64())
	rawdb.WriteHeadBlockHash(batch, block.Hash())
	// store cur block
	// bc.currentBlock.Store(block)

	if strings.Compare(block.Coinbase().String(), bc.chainConfig.SysName) == 0 {
		rawdb.WriteIrreversibleNumber(batch, block.NumberU64())
		bc.irreversibleNumber.Store(block.NumberU64())
	}
}

// Genesis retrieves the chain's genesis block.
func (bc *BlockChain) Genesis() *types.Block {
	return bc.genesisBlock
}

// GetBody retrieves a block body (transactions ) from the database by hash, caching it if found.
func (bc *BlockChain) GetBody(hash common.Hash) *types.Body {
	if cached, ok := bc.bodyCache.Get(hash); ok {
		body := cached.(*types.Body)
		return body
	}
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	body := rawdb.ReadBody(bc.db, hash, *number)
	if body == nil {
		return nil
	}
	bc.bodyCache.Add(hash, body)
	return body
}

// GetBodyRLP retrieves a block body in RLP encoding from the database by hash, caching it if found.
func (bc *BlockChain) GetBodyRLP(hash common.Hash) rlp.RawValue {
	if cached, ok := bc.bodyRLPCache.Get(hash); ok {
		return cached.(rlp.RawValue)
	}
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}

	body := rawdb.ReadBodyRLP(bc.db, hash, *number)
	if len(body) == 0 {
		return nil
	}
	bc.bodyRLPCache.Add(hash, body)
	return body
}

// HasBlock checks if a block is fully present in the database or not.
func (bc *BlockChain) HasBlock(hash common.Hash, number uint64) bool {
	if bc.blockCache.Contains(hash) {
		return true
	}
	return rawdb.HasBody(bc.db, hash, number)
}

// HasState checks if state trie is fully present in the database or not.
func (bc *BlockChain) HasState(hash common.Hash) bool {
	return rawdb.ReadBlockStateOut(bc.db, hash) != nil
}

// HasBlockAndState checks if a block and  state  is fully present  in the database or not.
func (bc *BlockChain) HasBlockAndState(hash common.Hash, number uint64) bool {
	block := bc.GetBlock(hash, number)
	if block == nil {
		return false
	}
	return bc.HasState(hash)
}

// GetBlock retrieves a block from the database by hash and number, caching it if found.
func (bc *BlockChain) GetBlock(hash common.Hash, number uint64) *types.Block {
	if block, ok := bc.blockCache.Get(hash); ok {
		return block.(*types.Block)
	}
	block := rawdb.ReadBlock(bc.db, hash, number)
	if block == nil {
		return nil
	}
	bc.blockCache.Add(block.Hash(), block)
	return block
}

// GetBlockByHash retrieves a block from the database by hash, caching it if found.
func (bc *BlockChain) GetBlockByHash(hash common.Hash) *types.Block {
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return bc.GetBlock(hash, *number)
}

// GetBlockByNumber retrieves a block from the database by number, caching it if found.
func (bc *BlockChain) GetBlockByNumber(number uint64) *types.Block {
	hash := rawdb.ReadCanonicalHash(bc.db, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return bc.GetBlock(hash, number)
}

// GetReceiptsByHash retrieves the receipts for all transactions in a given block.
func (bc *BlockChain) GetReceiptsByHash(hash common.Hash) []*types.Receipt {
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return rawdb.ReadReceipts(bc.db, hash, *number)
}

// GetBlocksFromHash returns the block corresponding to hash and up to n-1 ancestors.
func (bc *BlockChain) GetBlocksFromHash(hash common.Hash, n int) (blocks []*types.Block) {
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	for i := 0; i < n; i++ {
		block := bc.GetBlock(hash, *number)
		if block == nil {
			break
		}
		blocks = append(blocks, block)
		hash = block.ParentHash()
		*number--
	}
	return
}

// Stop stops the blockchain service. If any imports are currently in progress
// it will abort them using the procInterrupt.
func (bc *BlockChain) Stop() {
	if !atomic.CompareAndSwapInt32(&bc.running, 0, 1) {
		return
	}
	close(bc.quit)
	atomic.StoreInt32(&bc.procInterrupt, 1)

	bc.wg.Wait()

	if bc.statePruning {
		triedb := bc.stateCache.TrieDB()
		if bc.dereferenceNumber != 0 {
			recent := bc.GetBlockByNumber(bc.dereferenceNumber + 1)
			log.Info("Writing cached state to disk", "block", recent.Number(), "hash", recent.Hash().String(), "root", recent.Root().String())
			if err := triedb.Commit(recent.Root(), true); err != nil {
				log.Error("Failed to commit recent state trie", "err", err)
			}
		}

		for !bc.triegc.Empty() {
			triedb.Dereference(bc.triegc.PopItem().(common.Hash))
		}
		if size, _ := triedb.Size(); size != 0 {
			log.Error("Dangling trie nodes after full cleanup")
		}
	}

	log.Info("Blockchain manager stopped")
}

func (bc *BlockChain) procFutureBlocks() {
	blocks := make([]*types.Block, 0, bc.futureBlocks.Len())
	for _, hash := range bc.futureBlocks.Keys() {
		if block, exist := bc.futureBlocks.Peek(hash); exist {
			blocks = append(blocks, block.(*types.Block))
		}
	}
	if len(blocks) > 0 {
		types.BlockBy(types.Number).Sort(blocks)
		for i := range blocks {
			bc.InsertChain(blocks[i : i+1])
		}
	}
}

// WriteBlockWithoutState writes only the block and its metadata to the database, but does not write any state.
func (bc *BlockChain) WriteBlockWithoutState(block *types.Block, td *big.Int) (err error) {
	bc.wg.Add(1)
	defer bc.wg.Done()
	if err := bc.WriteTd(block.Hash(), block.NumberU64(), td); err != nil {
		return err
	}
	rawdb.WriteBlock(bc.db, block)
	return nil
}

// WriteBlockWithState writes the block and all associated state to the database.
func (bc *BlockChain) WriteBlockWithState(block *types.Block, receipts []*types.Receipt, state *state.StateDB) (isCanon bool, err error) {
	bc.wg.Add(1)
	defer bc.wg.Done()

	ptd := bc.GetTd(block.ParentHash(), block.NumberU64()-1)
	if ptd == nil {
		return false, processor.ErrUnknownAncestor
	}
	// Make sure no inconsistent state is leaked during insertion
	bc.mu.Lock()
	defer bc.mu.Unlock()

	externTd := new(big.Int).Add(block.Difficulty(), ptd)
	if err := bc.WriteTd(block.Hash(), block.NumberU64(), externTd); err != nil {
		return false, err
	}

	// Write other block data using a batch.
	batch := bc.db.NewBatch()
	rawdb.WriteBlock(batch, block)

	root, err := state.Commit(batch, block.Hash(), block.NumberU64())
	if err != nil {
		return false, err
	}

	triedb := bc.stateCache.TrieDB()

	if !bc.statePruning {
		log.Debug("Tiredb commit", "root", root.String(), "number", block.NumberU64())
		if err := triedb.Commit(root, false); err != nil {
			return false, err
		}
		snapshotTime := (block.Time().Uint64() / bc.snapshotInterval) * bc.snapshotInterval
		bc.preSnapshotTime = snapshotTime
	} else {
		triedb.Reference(root, common.Hash{})
		bc.triegc.Push(root, -int64(block.NumberU64()))

		current := block.NumberU64()
		if current > bc.triesInMemory {
			var (
				nodes, imgs = triedb.Size()
				limit       = common.StorageSize(5) * 1024 * 1024
			)
			if nodes > limit || imgs > 4*1024*1024 {
				log.Debug("triedb.Cap")
				triedb.Cap(limit - fdb.IdealBatchSize)
			}
			chosen := current - bc.triesInMemory
			for !bc.triegc.Empty() {
				root, number := bc.triegc.Pop()
				log.Debug("Memory trie", "number", uint64(-number), "chosen", chosen)

				if uint64(-number) <= chosen && bc.statePruningClean {
					header := bc.GetHeaderByNumber(uint64(-number))
					snapshotTime := (header.Time.Uint64() / bc.snapshotInterval) * bc.snapshotInterval
					if header.Root == root.(common.Hash) && snapshotTime != bc.preSnapshotTime {
						log.Debug("Tiredb commit", "root", header.Root.String(), "number", uint64(-number), "time", header.Time.Uint64())
						triedb.Commit(header.Root, true)
						bc.preSnapshotTime = snapshotTime
						triedb.Dereference(root.(common.Hash))
					}
					triedb.Dereference(root.(common.Hash))
					bc.dereferenceNumber = uint64(-number)
				} else {
					if bc.statePruningClean == false {
						log.Debug("Tiredb commit", "root", root.(common.Hash).String(), "number", uint64(-number))
						triedb.Commit(root.(common.Hash), true)
						triedb.Dereference(root.(common.Hash))
					} else {
						bc.triegc.Push(root, number)
						break
					}
				}
			}

			if bc.statePruningClean == false {
				bc.statePruning = false
				bc.dereferenceNumber = 0
			}
		}
	}

	rawdb.WriteReceipts(batch, block.Hash(), block.NumberU64(), receipts)
	if bc.vmConfig.ContractLogFlag {
		detailtxs := make([]*types.DetailTx, len(receipts))
		for i := 0; i < len(receipts); i++ {
			detailtxs[i] = receipts[i].GetInternalTxsLog()
		}
		rawdb.WriteDetailTxs(batch, block.Hash(), block.NumberU64(), detailtxs)
	}

	currentBlock := bc.CurrentBlock()
	localTd := bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())

	reorg := externTd.Cmp(localTd) > 0 || strings.Compare(block.Coinbase().String(), bc.chainConfig.SysName) == 0
	if !reorg && externTd.Cmp(localTd) == 0 {
		// Split same-difficulty blocks by number, then at random
		reorg = block.NumberU64() < currentBlock.NumberU64() || (block.NumberU64() == currentBlock.NumberU64() && mrand.Float64() < 0.5)
	}

	if reorg {
		// Reorganise the chain if the parent is not the head block
		if block.ParentHash() != currentBlock.Hash() {
			if err := bc.reorgChain(currentBlock, block, batch); err != nil {
				if err == errReorgSystemBlock {
					goto Target
				}
				return false, err
			}

		}

		// Write the positional metadata for transaction/receipt lookups and preimages
		rawdb.WriteTxLookupEntries(batch, block)
		rawdb.WritePreimages(batch, block.NumberU64(), state.Preimages())
		isCanon = true
	}

	if isCanon {
		bc.insert(batch, block)
	}

Target:
	if err := batch.Write(); err != nil {
		return false, err
	}

	if isCanon {
		bc.currentBlock.Store(block)
	}

	bc.futureBlocks.Remove(block.Hash())

	return isCanon, nil
}

// StatePruning enale/disable state pruning
func (bc *BlockChain) StatePruning(enable bool) (bool, uint64) {
	log.Debug("Set State Pruning", "pruning", enable, "number", bc.CurrentBlock().NumberU64())
	tmp := bc.statePruning
	if enable {
		bc.statePruningClean = true
		bc.statePruning = true
	} else {
		bc.statePruningClean = false
	}
	return tmp, bc.CurrentBlock().NumberU64()
}

// InsertChain attempts to insert the given batch of blocks in to the canonical chain or, otherwise, create a fork.
func (bc *BlockChain) InsertChain(chain types.Blocks) (int, error) {
	n, _, err := bc.insertChain(chain)
	return n, err
}

// sanitycheck that the provided chain is actually ordered and linked
func (bc *BlockChain) sanityCheck(chain types.Blocks) error {
	for i := 1; i < len(chain); i++ {
		if chain[i].NumberU64() != chain[i-1].NumberU64()+1 || chain[i].ParentHash() != chain[i-1].Hash() {
			log.Error("Non contiguous block insert", "number", chain[i].Number(), "hash", chain[i].Hash(),
				"parent", chain[i].ParentHash(), "prevnumber", chain[i-1].Number(), "prevhash", chain[i-1].Hash())
			return fmt.Errorf("non contiguous insert: item %d is #%d [%x…], item %d is #%d [%x…] (parent [%x…])", i-1, chain[i-1].NumberU64(),
				chain[i-1].Hash().Bytes()[:4], i, chain[i].NumberU64(), chain[i].Hash().Bytes()[:4], chain[i].ParentHash().Bytes()[:4])
		}
	}
	return nil
}

// insertChain will execute the actual chain insertion and event aggregation.
func (bc *BlockChain) insertChain(chain types.Blocks) (int, []*types.Log, error) {
	if len(chain) == 0 {
		return 0, nil, nil
	}

	if err := bc.sanityCheck(chain); err != nil {
		return 0, nil, err
	}

	bc.wg.Add(1)
	defer bc.wg.Done()

	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	var (
		stats         = insertStats{startTime: time.Now()}
		coalescedLogs []*types.Log
	)

	if bc.senderCacher != nil {
		bc.senderCacher.RecoverFromBlocks(types.MakeSigner(bc.chainConfig.ChainID), chain)
	}

	// Iterate over the blocks and insert when the verifier permits
	for i, block := range chain {
		if atomic.LoadInt32(&bc.procInterrupt) == 1 {
			log.Debug("Premature abort during blocks processing")
			break
		}
		err := bc.validator.ValidateHeader(block.Header(), true)
		if err == nil {
			err = bc.Validator().ValidateBody(block)
		}
		switch {
		case err == processor.ErrKnownBlock:
			if bc.CurrentBlock().NumberU64() >= block.NumberU64() {
				stats.ignored++
				continue
			}
		case err == processor.ErrFutureBlock:
			max := big.NewInt(time.Now().Unix() + maxTimeFutureBlocks)
			if block.Time().Cmp(max) > 0 {
				return i, coalescedLogs, fmt.Errorf("future block: %v > %v", block.Time(), max)
			}
			bc.futureBlocks.Add(block.Hash(), block)
			stats.ignored++
			stats.queued++
			continue
		case err == processor.ErrUnknownAncestor && bc.futureBlocks.Contains(block.ParentHash()):
			bc.futureBlocks.Add(block.Hash(), block)
			stats.ignored++
			stats.queued++
			continue
		case err == processor.ErrPrunedAncestor:
			coalescedLogs, err := bc.insertSideChain(block)
			if err != nil {
				return i, coalescedLogs, err
			}
			continue
		case err != nil:
			bc.reportBlock(block, nil, err)
			return i, coalescedLogs, err
		}

		var parent *types.Block

		if i == 0 {
			parent = bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
		} else {
			parent = chain[i-1]
		}

		state, err := state.New(parent.Root(), bc.stateCache)
		if err != nil {
			return i, coalescedLogs, err
		}

		receipts, logs, usedGas, err := bc.processor.Process(block, state, bc.vmConfig)
		if err != nil {
			bc.reportBlock(block, receipts, err)
			return i, coalescedLogs, err
		}

		err = bc.validator.ValidateState(block, parent, state, receipts, usedGas)
		if err != nil {
			bc.reportBlock(block, receipts, err)
			return i, coalescedLogs, err
		}

		isCanon, err := bc.WriteBlockWithState(block, receipts, state)
		if err != nil {
			return i, coalescedLogs, err
		}

		if isCanon {
			log.Debug("Inserted new block", "number", block.Number(), "hash", block.Hash(),
				"txs", len(block.Transactions()), "gas", block.GasUsed())
			coalescedLogs = append(coalescedLogs, logs...)
			event.SendEvent(&event.Event{Typecode: event.ChainHeadEv, Data: block})
		} else {
			log.Debug("Inserted forked block", "number", block.Number(), "hash", block.Hash(), "diff", block.Difficulty(),
				"txs", len(block.Transactions()), "gas", block.GasUsed())
		}

		stats.processed++
		stats.txsCnt += len(block.Txs)
		stats.usedGas += usedGas
		stats.report(chain, i)

	}

	return 0, coalescedLogs, nil
}

func (bc *BlockChain) insertSideChain(block *types.Block) ([]*types.Log, error) {
	var systemBlock bool
	if strings.Compare(block.Coinbase().String(), bc.chainConfig.SysName) == 0 {
		systemBlock = true
	}

	currentBlock := bc.CurrentBlock()
	localTd := bc.GetTd(currentBlock.Hash(), currentBlock.NumberU64())
	externTd := new(big.Int).Add(bc.GetTd(block.ParentHash(), block.NumberU64()-1), block.Difficulty())
	if localTd.Cmp(externTd) >= 0 && !systemBlock {
		start := time.Now()
		if err := bc.WriteBlockWithoutState(block, externTd); err != nil {
			return nil, err
		}

		log.Debug("Injected sidechain block", "number", block.Number(), "hash", block.Hash(),
			"diff", block.Difficulty(), "elapsed", common.PrettyDuration(time.Since(start)),
			"txs", len(block.Transactions()), "gas", block.GasUsed(), "root", block.Root())
	} else {
		var blocks []*types.Block
		blocks = append(blocks, block)
		parent := bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
		for parent != nil && !bc.HasState(parent.Root()) {
			blocks = append(blocks, parent)
			parent = bc.GetBlock(block.ParentHash(), block.NumberU64()-1)
		}

		for j := 0; j < len(blocks)/2; j++ {
			blocks[j], blocks[len(blocks)-1-j] = blocks[len(blocks)-1-j], blocks[j]
		}

		bc.chainmu.Unlock()
		log.Info("Importing sidechain segment", "start", blocks[0].NumberU64(), "end", blocks[len(blocks)-1].NumberU64())
		_, logs, err := bc.insertChain(blocks)
		bc.chainmu.Lock()
		if err != nil {
			return logs, err
		}
	}
	return nil, nil

}

func (bc *BlockChain) reorgChain(oldBlock, newBlock *types.Block, batch fdb.Batch) error {
	var (
		newChain    types.Blocks
		oldChain    types.Blocks
		commonBlock *types.Block
		deletedTxs  []*types.Transaction
	)

	if oldBlock.NumberU64() > newBlock.NumberU64() {
		for ; oldBlock != nil && oldBlock.NumberU64() != newBlock.NumberU64(); oldBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1) {
			oldChain = append(oldChain, oldBlock)
			deletedTxs = append(deletedTxs, oldBlock.Txs...)
		}
	} else {
		for ; newBlock != nil && newBlock.NumberU64() != oldBlock.NumberU64(); newBlock = bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1) {
			newChain = append(newChain, newBlock)
		}
	}

	if oldBlock == nil {
		return fmt.Errorf("reorg chain not found oldblock ")
	}
	if newBlock == nil {
		return fmt.Errorf("reorg chain not found newblock ")
	}

	for {
		if oldBlock.Hash() == newBlock.Hash() {
			commonBlock = oldBlock
			break
		}
		oldChain = append(oldChain, oldBlock)
		newChain = append(newChain, newBlock)
		deletedTxs = append(deletedTxs, oldBlock.Txs...)
		oldBlock, newBlock = bc.GetBlock(oldBlock.ParentHash(), oldBlock.NumberU64()-1), bc.GetBlock(newBlock.ParentHash(), newBlock.NumberU64()-1)
		if oldBlock == nil {
			return fmt.Errorf("reorg chain not found old block ")
		}
		if newBlock == nil {
			return fmt.Errorf("reorg chain not found new block ")
		}
	}

	// Ensure the user sees large reorgs
	if len(oldChain) > 0 && len(newChain) > 0 {
		if oldChain[len(oldChain)-1].NumberU64() <= bc.IrreversibleNumber() {
			log.Warn("Do not accept other candidate fork the system chain", "hash", newBlock.Hash(), "coinbase", newBlock.Coinbase())
			return errReorgSystemBlock
		}

		logFn := log.Debug
		if len(oldChain) > 63 {
			logFn = log.Warn
		}
		logFn("Chain split detected", "number", commonBlock.Number(), "hash", commonBlock.Hash(), "drop", len(oldChain), "dropNum", oldChain[0].NumberU64(),
			"dropfrom", oldChain[0].Hash(), "add", len(newChain), "addNum", newChain[0].NumberU64(), "addfrom", newChain[0].Hash())
	} else {
		log.Error("Impossible reorg, please file an issue", "oldnum", oldBlock.Number(), "oldhash", oldBlock.Hash(), "newnum", newBlock.Number(), "newhash", newBlock.Hash())
	}

	var addedTxs []*types.Transaction
	for i := len(newChain) - 1; i >= 0; i-- {
		bc.insert(batch, newChain[i])
		rawdb.WriteTxLookupEntries(batch, newChain[i])
		addedTxs = append(addedTxs, newChain[i].Txs...)
	}
	diff := types.TxDifference(deletedTxs, addedTxs)
	for _, tx := range diff {
		rawdb.DeleteTxLookupEntry(batch, tx.Hash())
	}

	return nil
}

func (bc *BlockChain) update() {
	futureTimer := time.NewTicker(5 * time.Second)
	defer futureTimer.Stop()
	for {
		select {
		case <-futureTimer.C:
			bc.procFutureBlocks()
		case <-bc.quit:
			return
		}
	}
}

// BadBlocks returns a list of the last 'bad blocks' that the client has seen on the network
func (bc *BlockChain) BadBlocks() []*types.Block {
	blocks := make([]*types.Block, 0, bc.badBlocks.Len())
	for _, hash := range bc.badBlocks.Keys() {
		if blk, exist := bc.badBlocks.Peek(hash); exist {
			block := blk.(*types.Block)
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// addBadBlock adds a bad block to the bad-block LRU cache
func (bc *BlockChain) addBadBlock(block *types.Block) {
	bc.badBlocks.Add(block.Hash(), block)
}

// reportBlock logs a bad block error.
func (bc *BlockChain) reportBlock(block *types.Block, receipts []*types.Receipt, err error) {
	bc.addBadBlock(block)
	log.Error(fmt.Sprintf(`
########## BAD BLOCK #########
Error: %v

Chain config: %v

Number: %v
Hash: %v

##############################
`, err, bc.chainConfig, block.NumberU64(), block.Hash().Hex()))
}

// GetBlockNumber retrieves the block number belonging to the given hash from the cache or database
func (bc *BlockChain) GetBlockNumber(hash common.Hash) *uint64 {
	if cached, ok := bc.numberCache.Get(hash); ok {
		number := cached.(uint64)
		return &number
	}
	number := rawdb.ReadHeaderNumber(bc.db, hash)
	if number != nil {
		bc.numberCache.Add(hash, *number)
	}
	return number
}

// GetTd retrieves a block's total difficulty in the canonical chain from the database by hash and number, caching it if found.
func (bc *BlockChain) GetTd(hash common.Hash, number uint64) *big.Int {
	if cached, ok := bc.tdCache.Get(hash); ok {
		return cached.(*big.Int)
	}
	td := rawdb.ReadTd(bc.db, hash, number)
	if td == nil {
		return nil
	}
	bc.tdCache.Add(hash, td)
	return td
}

// GetTdByHash retrieves a block's total difficulty in the canonical chain from the database by hash, caching it if found.
func (bc *BlockChain) GetTdByHash(hash common.Hash) *big.Int {
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return bc.GetTd(hash, *number)
}

// WriteTd stores a block's total difficulty into the database, also caching it along the way.
func (bc *BlockChain) WriteTd(hash common.Hash, number uint64, td *big.Int) error {
	rawdb.WriteTd(bc.db, hash, number, td)
	bc.tdCache.Add(hash, new(big.Int).Set(td))
	return nil
}

// CurrentHeader retrieves the current head header of the canonical chain.
func (bc *BlockChain) CurrentHeader() *types.Header {
	return bc.CurrentBlock().Header()
}

// GetHeader retrieves a block header from the database by hash and number, caching it if found.
func (bc *BlockChain) GetHeader(hash common.Hash, number uint64) *types.Header {
	if header, ok := bc.headerCache.Get(hash); ok {
		return header.(*types.Header)
	}
	header := rawdb.ReadHeader(bc.db, hash, number)
	if header == nil {
		return nil
	}
	bc.headerCache.Add(hash, header)
	return header
}

// GetHeaderByHash retrieves a block header from the database by hash, caching it if found.
func (bc *BlockChain) GetHeaderByHash(hash common.Hash) *types.Header {
	number := bc.GetBlockNumber(hash)
	if number == nil {
		return nil
	}
	return bc.GetHeader(hash, *number)

}

// HasHeader checks if a block header is present in the database or not.
func (bc *BlockChain) HasHeader(hash common.Hash, number uint64) bool {
	if bc.numberCache.Contains(hash) || bc.headerCache.Contains(hash) {
		return true
	}
	return rawdb.HasHeader(bc.db, hash, number)

}

// GetBlockHashesFromHash retrieves a number of block hashes starting at a given hash, fetching towards the genesis block.
func (bc *BlockChain) GetBlockHashesFromHash(hash common.Hash, max uint64) []common.Hash {
	header := bc.GetHeaderByHash(hash)
	if header == nil {
		return nil
	}
	chain := make([]common.Hash, 0, max)
	for i := uint64(0); i < max; i++ {
		next := header.ParentHash
		if header = bc.GetHeader(next, header.Number.Uint64()-1); header == nil {
			break
		}
		chain = append(chain, next)
		if header.Number.Sign() == 0 {
			break
		}
	}
	return chain
}

// GetAncestor retrieves the Nth ancestor of a given block.
func (bc *BlockChain) GetAncestor(hash common.Hash, number, ancestor uint64, maxNonCanonical *uint64) (common.Hash, uint64) {
	bc.chainmu.Lock()
	defer bc.chainmu.Unlock()

	if ancestor > number {
		return common.Hash{}, 0
	}
	if ancestor == 1 {
		// in this case it is cheaper to just read the header
		if header := bc.GetHeader(hash, number); header != nil {
			return header.ParentHash, number - 1
		}
		return common.Hash{}, 0
	}
	for ancestor != 0 {
		if rawdb.ReadCanonicalHash(bc.db, number) == hash {
			number -= ancestor
			return rawdb.ReadCanonicalHash(bc.db, number), number
		}
		if *maxNonCanonical == 0 {
			return common.Hash{}, 0
		}
		*maxNonCanonical--
		ancestor--
		header := bc.GetHeader(hash, number)
		if header == nil {
			return common.Hash{}, 0
		}
		hash = header.ParentHash
		number--
	}
	return hash, number

}

// GetHeaderByNumber retrieves a block header from the database by number.
func (bc *BlockChain) GetHeaderByNumber(number uint64) *types.Header {
	hash := rawdb.ReadCanonicalHash(bc.db, number)
	if hash == (common.Hash{}) {
		return nil
	}
	return bc.GetHeader(hash, number)
}

// Config retrieves the blockchain's chain configuration.
func (bc *BlockChain) Config() *params.ChainConfig { return bc.chainConfig }

// CalcGasLimit computes the gas limit of the next block after parent.
func (bc *BlockChain) CalcGasLimit(parent *types.Block) uint64 {
	return params.CalcGasLimit(parent)
}

// ForkUpdate .
func (bc *BlockChain) ForkUpdate(block *types.Block, statedb *state.StateDB) error {
	return bc.fcontroller.update(block, statedb)
}

// Export writes the active chain to the given writer.
func (bc *BlockChain) Export(w io.Writer) error {
	return bc.ExportN(w, uint64(0), bc.CurrentBlock().NumberU64())
}

// ExportN writes a subset of the active chain to the given writer.
func (bc *BlockChain) ExportN(w io.Writer, first uint64, last uint64) error {
	bc.chainmu.RLock()
	defer bc.chainmu.RUnlock()

	if first > last {
		return fmt.Errorf("export failed: first (%d) is greater than last (%d)", first, last)
	}
	log.Info("Exporting batch of blocks", "count", last-first+1)

	start, reported := time.Now(), time.Now()
	for nr := first; nr <= last; nr++ {
		block := bc.GetBlockByNumber(nr)
		if block == nil {
			return fmt.Errorf("export failed on #%d: not found", nr)
		}
		if err := block.ExtEncodeRLP(w); err != nil {
			return err
		}
		if time.Since(reported) >= 8*time.Second {
			log.Info("Exporting blocks", "exported", block.NumberU64()-first, "elapsed", common.PrettyDuration(time.Since(start)))
			reported = time.Now()
		}
	}
	return nil
}

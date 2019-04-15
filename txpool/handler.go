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

package txpool

import (
	"sync/atomic"
	"time"

	"github.com/fractalplatform/fractal/common"
	router "github.com/fractalplatform/fractal/event"
	"github.com/fractalplatform/fractal/types"
)

const (
	maxKonwnTxs      = 32
	txsSendDelay     = 50 * time.Millisecond
	txsSendThreshold = 32
)

type hashQueue struct {
	write    int
	elements [maxKonwnTxs]common.Hash
}

func (q *hashQueue) add(hash common.Hash) {
	q.elements[q.write] = hash
	q.write++
	q.write %= len(q.elements)
}

func (q *hashQueue) has(hash common.Hash) bool {
	write := q.write
	for {
		if q.elements[write] == hash {
			return true
		}
		write--
		if write < 0 {
			write = len(q.elements) - 1
		}
		if write == q.write {
			break
		}
	}
	return false
}

type peerInfo struct {
	knownTxs hashQueue
	peer     router.Station
	idle     int32
}

func (p *peerInfo) setIdle() {
	atomic.StoreInt32(&p.idle, 0)
}

func (p *peerInfo) setBusy() bool {
	return atomic.CompareAndSwapInt32(&p.idle, 0, 1)
}

func (p *peerInfo) addTxs(txs []*types.Transaction) {
	for _, tx := range txs {
		p.knownTxs.add(tx.Hash())
	}
}

func (p *peerInfo) hadTxs(tx *types.Transaction) bool {
	return p.knownTxs.has(tx.Hash())
}

/*
type peerInfo struct {
	knownTxs mapset.Set
	peer     router.Station
}

func (p *peerInfo) addTxs(txs []*types.Transaction) {
	for _, tx := range txs {
		p.knownTxs.Add(tx.Hash())
	}
	for p.knownTxs.Cardinality() >= maxKonwnTxs {
		p.knownTxs.Pop()
	}
}

func (p *peerInfo) hadTxs(tx *types.Transaction) bool {
	return p.knownTxs.Contains(tx.Hash())
}
*/

type hashPath struct {
	hash common.Hash
	path []string
}

type TxpoolStation struct {
	station router.Station
	txChan  chan *router.Event
	txpool  *TxPool
	peers   map[string]*peerInfo
	path    [256]*hashPath
}

type TransactionWithPath struct {
	Tx   *types.Transaction
	Path []string
}

func NewTxpoolStation(txpool *TxPool) *TxpoolStation {
	station := &TxpoolStation{
		station: router.NewLocalStation("txpool", nil),
		txChan:  make(chan *router.Event),
		txpool:  txpool,
		peers:   make(map[string]*peerInfo),
	}
	router.Subscribe(nil, station.txChan, router.P2PTxMsg, []*TransactionWithPath{}) // recive txs form remote
	router.Subscribe(nil, station.txChan, router.NewPeerPassedNotify, nil)           // new peer is handshake completed
	router.Subscribe(nil, station.txChan, router.DelPeerNotify, new(string))         // new peer is handshake completed
	router.Subscribe(nil, station.txChan, router.NewTxs, []*types.Transaction{})     // NewTxs recived , prepare to broadcast
	go station.handleMsg()
	return station
}

func (s *TxpoolStation) addTx(tx *types.Transaction, path []string) {
	hash := tx.Hash()
	index := hash[0]
	if s.path[index] == nil {
		s.path[index] = &hashPath{hash: hash, path: path}
	} else if s.path[index].hash != hash {
		s.path[index].hash = hash
		s.path[index].path = path
		return
	}
	s.path[index].path = append(s.path[index].path, path...)
}

func (s *TxpoolStation) addTxs(txs []*TransactionWithPath, from string) {
	for _, tx := range txs {
		s.addTx(tx.Tx, append(tx.Path, from))
	}
}

func (s *TxpoolStation) getTxPath(tx *types.Transaction) []string {
	hash := tx.Hash()
	index := hash[0]
	if s.path[index] == nil || s.path[index].hash != hash {
		return nil
	}
	return s.path[index].path
}

func (s *TxpoolStation) existPath(tx *types.Transaction, path string) bool {
	hash := tx.Hash()
	index := hash[0]
	if s.path[index] == nil || s.path[index].hash != hash {
		return false
	}
	for _, p := range s.path[index].path {
		if p == path {
			return true
		}
	}
	return false
}

var hasTx = 0
var existTx = 0

func (s *TxpoolStation) broadcast(txs []*types.Transaction) {
	txFirst := txs[0]
	peers := make([]*peerInfo, 0, 3)
	index := 0
	for name, peerInfo := range s.peers {
		if peerInfo.hadTxs(txFirst) {
			router.Println("has tx", hasTx)
			hasTx++
			continue
		}
		if s.existPath(txFirst, name) {
			router.Println("existPath", existTx)
			existTx++
			continue
		}
		if !peerInfo.setBusy() {
			continue
		}
		peers = append(peers, peerInfo)
		peerInfo.addTxs(txs)
		s.addTx(txFirst, []string{name})
		index++
		if index >= 3 {
			break
		}
	}
	router.Println("Broadcast num:", len(peers), len(s.peers))
	if len(peers) == 0 {
		return
	}
	txsSend := &TransactionWithPath{
		Tx:   txFirst,
		Path: s.getTxPath(txFirst),
	}
	go func() {
		start := router.TimeMs()
		defer func() {
			router.Println("txs Broadcast:", len(peers), len(s.peers), router.TimeMs()-start)
		}()
		for _, peer := range peers {
			router.SendTo(nil, peer.peer, router.P2PTxMsg, []*TransactionWithPath{
				txsSend,
			})
			peer.setIdle()
		}
	}()
}

/*
func (s *TxpoolStation) broadcast(txs []*types.Transaction) {
	txMid, txFirst, txLast := txs[0], txs[len(txs)-1], txs[len(txs)/2]
	sendCount := 0
	for _, peerInfo := range s.peers {
		if peerInfo.hadTxs(txMid) && peerInfo.hadTxs(txFirst) && peerInfo.hadTxs(txLast) {
			continue
		}
		router.SendTo(nil, peerInfo.peer, router.P2PTxMsg, txs)
		peerInfo.addTxs(txs)
		sendCount++
		if sendCount > 3 {
			break
		}
	}
}
*/
func (s *TxpoolStation) handleMsg() {
	for {
		e := <-s.txChan
		switch e.Typecode {
		case router.NewTxs:
			txs := e.Data.([]*types.Transaction)
			s.broadcast(txs)
		case router.P2PTxMsg:
			txs := e.Data.([]*TransactionWithPath)
			peerInfo := s.peers[e.From.Name()]
			s.addTxs(txs, e.From.Name())
			rawTxs := make([]*types.Transaction, len(txs))
			for i := range txs {
				rawTxs[i] = txs[i].Tx
			}
			if peerInfo != nil {
				peerInfo.addTxs(rawTxs)
			}
			go s.txpool.AddRemotes(rawTxs)
		case router.NewPeerPassedNotify:
			newpeer := &peerInfo{peer: e.From, idle: 1}
			//s.peers[e.From.Name()] = &peerInfo{knownTxs: mapset.NewSet(), peer: e.From}
			s.peers[e.From.Name()] = newpeer
			router.Printf("add new peers:%x\n", e.From.Name())
			s.syncTransactions(newpeer)
		case router.DelPeerNotify:
			delete(s.peers, e.From.Name())
		}
	}
}

/*
func (s *TxpoolStation) handleMsg() {
	InTxsCache := make([]*types.Transaction, 0, 64)
	OutTxsCache := make([]*types.Transaction, 0, 64)
	CacheTriger := make(chan struct{}, 1)
	timer := time.NewTimer(time.Second)
	timer.Stop()
	triggerHandle := func() {
		select {
		case CacheTriger <- struct{}{}:
		default:
		}
	}
	for {
		select {
		case <-timer.C:
			triggerHandle()
		case <-CacheTriger:
			if len(InTxsCache) > 0 {
				go s.txpool.AddRemotes(InTxsCache)
				InTxsCache = InTxsCache[:0]
			}
			if len(OutTxsCache) > 0 {
				s.broadcast(OutTxsCache)
				OutTxsCache = OutTxsCache[:0]
			}
		case e := <-s.txChan:
			switch e.Typecode {
			case router.NewTxs:
				txs := e.Data.([]*types.Transaction)
				OutTxsCache = append(OutTxsCache, txs...)
				if len(OutTxsCache) > txsSendThreshold {
					triggerHandle()
					break
				}
				timer.Stop()
				timer.Reset(txsSendDelay)
			case router.P2PTxMsg:
				txs := e.Data.([]*types.Transaction)
				peerInfo := s.peers[e.From.Name()]
				peerInfo.addTxs(txs)
				InTxsCache = append(InTxsCache, txs...)
				if len(InTxsCache) > txsSendThreshold {
					triggerHandle()
					break
				}
				timer.Stop()
				timer.Reset(txsSendDelay)
			case router.NewPeerPassedNotify:
				s.peers[e.From.Name()] = &peerInfo{knownTxs: mapset.NewSet(), peer: e.From}
				go s.syncTransactions(e)
			case router.DelPeerNotify:
				delete(s.peers, e.From.Name())
			}
		}
	}
}
*/

func (s *TxpoolStation) syncTransactions(peer *peerInfo) {
	var txs []*TransactionWithPath
	pending, _ := s.txpool.Pending()
	for _, batch := range pending {
		for _, tx := range batch {
			txs = append(txs, &TransactionWithPath{Tx: tx, Path: s.getTxPath(tx)})
		}
	}
	if len(txs) == 0 {
		return
	}
	go func() {
		router.SendTo(nil, peer.peer, router.P2PTxMsg, txs)
		peer.setIdle()
	}()
}

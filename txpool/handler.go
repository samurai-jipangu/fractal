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
	"time"

	mapset "github.com/deckarep/golang-set"
	router "github.com/fractalplatform/fractal/event"
	"github.com/fractalplatform/fractal/types"
)

const (
	maxKonwnTxs      = 1024
	txsSendDelay     = 50 * time.Millisecond
	txsSendThreshold = 32
)

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

type TxpoolStation struct {
	station router.Station
	txChan  chan *router.Event
	txpool  *TxPool
	peers   map[string]*peerInfo
}

func NewTxpoolStation(txpool *TxPool) *TxpoolStation {
	station := &TxpoolStation{
		station: router.NewLocalStation("txpool", nil),
		txChan:  make(chan *router.Event),
		txpool:  txpool,
		peers:   make(map[string]*peerInfo),
	}
	router.Subscribe(nil, station.txChan, router.P2PTxMsg, []*types.Transaction{}) // recive txs form remote
	router.Subscribe(nil, station.txChan, router.NewPeerPassedNotify, nil)         // new peer is handshake completed
	router.Subscribe(nil, station.txChan, router.DelPeerNotify, new(string))       // new peer is handshake completed
	router.Subscribe(nil, station.txChan, router.NewTxs, []*types.Transaction{})   // NewTxs recived , prepare to broadcast
	go station.handleMsg()
	return station
}

func (s *TxpoolStation) broadcast(txs []*types.Transaction) {
	txFirst := txs[0]
	sendCount := 0
	for _, peerInfo := range s.peers {
		if peerInfo.hadTxs(txFirst) {
			continue
		}
		router.SendTo(nil, peerInfo.peer, router.P2PTxMsg, txs)
		peerInfo.addTxs(txs)
		sendCount++
		if sendCount > 5 {
			break
		}
	}
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
			txs := e.Data.([]*types.Transaction)
			peerInfo := s.peers[e.From.Name()]
			peerInfo.addTxs(txs)
			s.txpool.AddRemotes(txs)
		case router.NewPeerPassedNotify:
			s.peers[e.From.Name()] = &peerInfo{knownTxs: mapset.NewSet(), peer: e.From}
			go s.syncTransactions(e)
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

func (s *TxpoolStation) syncTransactions(e *router.Event) {
	var txs []*types.Transaction
	pending, _ := s.txpool.Pending()
	for _, batch := range pending {
		txs = append(txs, batch...)
	}
	if len(txs) == 0 {
		return
	}
	router.SendTo(nil, e.From, router.P2PTxMsg, txs)
}

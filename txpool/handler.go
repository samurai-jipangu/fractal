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

	router "github.com/fractalplatform/fractal/event"
	"github.com/fractalplatform/fractal/types"
)

type TxpoolStation struct {
	station router.Station
	txChan  chan *router.Event
	txpool  *TxPool
}

func NewTxpoolStation(txpool *TxPool) *TxpoolStation {
	station := &TxpoolStation{
		station: router.NewLocalStation("txpool", nil),
		txChan:  make(chan *router.Event),
		txpool:  txpool,
	}
	router.Subscribe(nil, station.txChan, router.P2PTxMsg, []*types.Transaction{}) // recive txs form remote
	router.Subscribe(nil, station.txChan, router.NewPeerPassedNotify, nil)         // new peer is handshake completed
	router.Subscribe(nil, station.txChan, router.NewTxs, []*types.Transaction{})   // NewTxs recived , prepare to broadcast
	go station.handleMsg()
	return station
}

func (s *TxpoolStation) handleMsg() {
	InTxsCache := make([]*types.Transaction, 0, 1024)
	OutTxsCache := make([]*types.Transaction, 0, 1024)
	CacheTriger := make(chan struct{})
	timer := time.NewTimer(time.Second)
	for {
		select {
		case <-timer.C:
			CacheTriger <- struct{}{}
		case <-CacheTriger:
			if len(InTxsCache) > 0 {
				go s.txpool.AddRemotes(InTxsCache)
				InTxsCache = InTxsCache[:0]
			}
			if len(OutTxsCache) > 0 {
				go router.SendEvent(&router.Event{To: router.GetStationByName("broadcast"), Typecode: router.P2PTxMsg, Data: OutTxsCache})
				OutTxsCache = OutTxsCache[:0]
			}
		case e := <-s.txChan:
			switch e.Typecode {
			case router.NewTxs:
				txs := e.Data.([]*types.Transaction)
				OutTxsCache = append(OutTxsCache, txs...)
				if len(OutTxsCache) > 1000 {
					CacheTriger <- struct{}{}
					break
				}
				timer.Stop()
				timer.Reset(time.Second)
			case router.P2PTxMsg:
				txs := e.Data.([]*types.Transaction)
				InTxsCache = append(InTxsCache, txs...)
				if len(InTxsCache) > 1000 {
					CacheTriger <- struct{}{}
					break
				}
				timer.Stop()
				timer.Reset(time.Second)
			case router.NewPeerPassedNotify:
				go s.syncTransactions(e)
			}
		}
	}
}

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

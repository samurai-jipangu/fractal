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
	router.Subscribe(nil, station.txChan, router.P2PTxMsg, []*types.Transaction{})
	router.Subscribe(nil, station.txChan, router.NewPeerPassedNotify, nil)
	go station.handleMsg()
	return station
}

var duration int64
var durmax int64
var durcount int64
var txscount int64

func (s *TxpoolStation) handleMsg() {
	for {
		e := <-s.txChan
		switch e.Typecode {
		case router.P2PTxMsg:
			start := time.Now().Unix()
			txs := e.Data.([]*types.Transaction)
			s.txpool.AddRemotes(txs)
			dur := time.Now().Unix() - start
			durcount++
			txscount += int64(len(txs))
			if durmax < dur {
				durmax = dur
			}
			duration += dur
			if durcount%1024 == 0 {
				router.Println("Txpool max/avg/count/txs/per:", durmax, duration/durcount, durcount, txscount, len(txs))
			}
		case router.NewPeerPassedNotify:
			go s.syncTransactions(e)
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

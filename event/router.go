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

package event

import (
	"fmt"
	"reflect"
	"sync"
	"time"
)

var printLock sync.Mutex

func TimeMs() int64 {
	return time.Now().UnixNano() / 1e6
}
func TimeUs() int64 {
	return time.Now().UnixNano() / 1e3
}

func Println(args ...interface{}) {
	//return
	//	printLock.Lock()
	fmt.Printf("%d:", TimeMs())
	fmt.Println(args...)
	//	printLock.Unlock()
}

func Printf(sfmt string, args ...interface{}) {
	//return
	//	printLock.Lock()
	fmt.Printf("%d:", TimeMs())
	fmt.Printf(sfmt, args...)
	//	printLock.Unlock()
}

// ProtoAdaptor used to send out event
type ProtoAdaptor interface {
	SendOut(*Event) error
}

// Router Router all events
type Router struct {
	namedFeeds   map[string]map[int]*Feed
	namedMutex   sync.RWMutex
	unnamedFeeds map[int]*Feed
	adaptor      ProtoAdaptor
	unnamedMutex sync.RWMutex
	stations     map[string]Station
	stationMutex sync.RWMutex
}

var router *Router

// InitRouter init router.
func init() {
	router = New()
}

// New returns an initialized Router instance.
func New() *Router {
	return &Router{
		unnamedFeeds: make(map[int]*Feed),
		namedFeeds:   make(map[string]map[int]*Feed),
		stations:     make(map[string]Station),
	}
}

// Reset ntended for testing。
func Reset() {
	router = New()
}

// Event is including normal event and p2p event
type Event struct {
	From     Station
	To       Station
	Typecode int
	Data     interface{}
}

// Type enumerator
const (
	P2PRouterTestInt            int = iota // 0
	P2PRouterTestInt64                     // 1
	P2PRouterTestString                    // 2
	P2PRouterTestNewPeer                   // 3 fixed bug
	P2PRouterTestDelPeer                   // 4 fixed bug
	P2PRouterTestDisconnectPeer            // 5 fixed bug
	P2PGetStatus                           // 6 Status request
	P2PStatusMsg                           // 7 Status response
	P2PGetBlockHashMsg                     // 8 BlockHash request
	P2PGetBlockHeadersMsg                  // 9 BlockHeader request
	P2PGetBlockBodiesMsg                   // 10 BlockBodies request
	P2PBlockHeadersMsg                     // 11 BlockHeader response
	P2PBlockBodiesMsg                      // 12 BlockBodies response
	P2PBlockHashMsg                        // 13 BlockHash response
	P2PNewBlockHashesMsg                   // 14 NewBlockHash notify
	P2PTxMsg                               // 15 TxMsg notify
	P2PEndSize
	ChainHeadEv         = 1023 + iota - P2PEndSize // 1024
	NewPeerNotify                                  // 1025
	DelPeerNotify                                  // 1026
	DisconectCtrl                                  // 1027
	NewPeerPassedNotify                            // 1028 same chain ID and gensis block
	TxEv                                           // 1029
	NewMinedEv                                     // 1030
	NewTxs                                         // 1031
	EndSize
)

var TypeName = [P2PEndSize]string{
	P2PRouterTestInt:            "-Int",            // 0
	P2PRouterTestInt64:          "-Int64",          // 1
	P2PRouterTestString:         "-String",         // 2
	P2PRouterTestNewPeer:        "-NewPeer",        // 3 fixed bug
	P2PRouterTestDelPeer:        "-DelPeer",        // 4 fixed bug
	P2PRouterTestDisconnectPeer: "-DisconnectCtrl", // 5 fixed bug
	P2PGetStatus:                "G-Status",        // 6 Status request
	P2PStatusMsg:                "R-Status",        // 7 Status response
	P2PGetBlockHashMsg:          "G-B-Hash",        // 8 BlockHash request
	P2PGetBlockHeadersMsg:       "G-H",             // 9 BlockHeader request
	P2PGetBlockBodiesMsg:        "G-B-Body",        // 10 BlockBodies request
	P2PBlockHeadersMsg:          "R-H",             // 11 BlockHeader response
	P2PBlockBodiesMsg:           "R-B-Body",        // 12 BlockBodies response
	P2PBlockHashMsg:             "R-B-Hash",        // 13 BlockHash response
	P2PNewBlockHashesMsg:        "N-B-Hash",        // 14 NewBlockHash notify
	P2PTxMsg:                    "N-TXs",           // 15 TxMsg notify
}

var typeListMutex sync.RWMutex
var typeList = [EndSize]reflect.Type{}

var typeLimit = [P2PEndSize]int{
	P2PGetStatus:          1,
	P2PGetBlockHashMsg:    128,
	P2PGetBlockHeadersMsg: 64,
	P2PGetBlockBodiesMsg:  64,
	P2PNewBlockHashesMsg:  3,
}

// ReplyEvent is equivalent to `SendTo(e.To, e.From, typecode, data)`
func ReplyEvent(e *Event, typecode int, data interface{}) {
	SendEvent(&Event{
		From:     e.To,
		To:       e.From,
		Typecode: typecode,
		Data:     data,
	})
}

// GetTypeByCode return Type by typecode
func GetTypeByCode(typecode int) reflect.Type {
	if typecode < P2PEndSize {
		typeListMutex.RLock()
		defer typeListMutex.RUnlock()
		return typeList[typecode]
	}
	return nil
}

func bindTypeToCode(typecode int, data interface{}) {
	if typecode >= EndSize {
		panic("dataType greater than EndSize!")
	}
	if data == nil {
		return
	}
	typ := reflect.TypeOf(data)
	typeListMutex.RLock()
	etyp := typeList[typecode]
	typeListMutex.RUnlock()
	if etyp == nil {
		typeListMutex.Lock()
		etyp = typeList[typecode]
		if etyp == nil {
			typeList[typecode] = typ
		}
		typeListMutex.Unlock()
		return
	}
	if etyp != typ {
		panic(fmt.Sprintf("%s mismatch %s!", typ.String(), etyp.String()))
	}
}

// GetStationByName retrun Station by Station's name
func GetStationByName(name string) Station { return router.GetStationByName(name) }
func (router *Router) GetStationByName(name string) Station {
	router.stationMutex.RLock()
	defer router.stationMutex.RUnlock()
	return router.stations[name]
}

// StationRegister register 'Station' to Router
func StationRegister(station Station) { router.StationRegister(station) }
func (router *Router) StationRegister(station Station) {
	router.stationMutex.Lock()
	router.stations[station.Name()] = station
	router.stationMutex.Unlock()
}

// StationUnregister unregister 'Station'
func StationUnregister(station Station) { router.StationUnregister(station) }
func (router *Router) StationUnregister(station Station) {
	router.stationMutex.Lock()
	delete(router.stations, station.Name())
	router.stationMutex.Unlock()
}

func (router *Router) bindChannelToStation(station Station, typecode int, channel chan *Event) Subscription {
	name := station.Name()
	router.namedMutex.Lock()
	_, ok := router.namedFeeds[name]
	if !ok {
		router.namedFeeds[name] = make(map[int]*Feed)
	}
	feed, ok := router.namedFeeds[name][typecode]
	if !ok {
		feed = &Feed{}
		router.namedFeeds[name][typecode] = feed
	}
	router.namedMutex.Unlock()
	return feed.Subscribe(channel)
}

func (router *Router) bindChannelToTypecode(typecode int, channel chan *Event) Subscription {
	router.unnamedMutex.Lock()
	feed, ok := router.unnamedFeeds[typecode]
	if !ok {
		feed = &Feed{}
		router.unnamedFeeds[typecode] = feed
	}
	router.unnamedMutex.Unlock()
	return feed.Subscribe(channel)
}

// Subscribe .
func Subscribe(station Station, channel chan *Event, typecode int, data interface{}) Subscription {
	return router.Subscribe(station, channel, typecode, data)
}
func (router *Router) Subscribe(station Station, channel chan *Event, typecode int, data interface{}) Subscription {

	bindTypeToCode(typecode, data)

	var sub Subscription

	if station != nil {
		sub = router.bindChannelToStation(station, typecode, channel)
	} else {
		sub = router.bindChannelToTypecode(typecode, channel)
	}
	return sub
}

// AdaptorRegister register P2P interface to Router
func AdaptorRegister(adaptor ProtoAdaptor) { router.AdaptorRegister(adaptor) }
func (router *Router) AdaptorRegister(adaptor ProtoAdaptor) {
	router.unnamedMutex.Lock()
	defer router.unnamedMutex.Unlock()
	if router.adaptor == nil {
		router.adaptor = adaptor
	}
}

// SendTo  is equivalent to SendEvent(&Event{From: from, To: to, Type: typecode, Data: data})
func SendTo(from, to Station, typecode int, data interface{}) int {
	return SendEvent(&Event{From: from, To: to, Typecode: typecode, Data: data})
}

// SendEvent send event
func SendEvent(e *Event) (nsent int) { return router.SendEvent(e) }
func (router *Router) SendEvent(e *Event) (nsent int) {

	//if e.Typecode >= EndSize || (typeList[e.Typecode] != nil && reflect.TypeOf(e.Data) != typeList[e.Typecode]) {
	//	fmt.Println("SendEvent Err:", e.Typecode, EndSize, reflect.TypeOf(e.Data), typeList[e.Typecode])
	//	panic("-")
	//return
	//}

	if e.To != nil {
		if e.To.IsRemote() {
			router.sendToAdaptor(e)
			return 1
		}
		//if len(e.To.Name()) != 0 {
		router.namedMutex.RLock()
		feeds, ok := router.namedFeeds[e.To.Name()]
		if ok {
			feed, ok := feeds[e.Typecode]
			if ok {
				nsent = feed.Send(e)
			}
		}
		router.namedMutex.RUnlock()
		return
		//}
	}

	router.unnamedMutex.RLock()
	if feed, ok := router.unnamedFeeds[e.Typecode]; ok {
		nsent = feed.Send(e)
	}
	router.unnamedMutex.RUnlock()
	return
}

func (router *Router) sendToAdaptor(e *Event) {
	router.unnamedMutex.RLock()
	if router.adaptor != nil {
		router.adaptor.SendOut(e)
	}
	router.unnamedMutex.RUnlock()
}

// SendEvents .
func SendEvents(es []*Event) (nsent int) {
	for _, e := range es {
		nsent += SendEvent(e)
	}
	return
}

//GetDDosLimit get messagetype req limit per second
func GetDDosLimit(t int) int {
	return typeLimit[t]
}

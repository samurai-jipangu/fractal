package protoadaptor

import (
	"fmt"
	"reflect"
	"time"

	"github.com/ethereum/go-ethereum/log"
	router "github.com/fractalplatform/fractal/event"
	"github.com/fractalplatform/fractal/p2p"
	"github.com/fractalplatform/fractal/utils/rlp"
)

type pack struct {
	From     string
	To       string
	Typecode uint32
	Payload  []byte
}

type remotePeer struct {
	peer *p2p.Peer
	ws   p2p.MsgReadWriter
}

// ProtoAdaptor is subprotocol on p2p
type ProtoAdaptor struct {
	p2p.Server
	peerMangaer
	event   chan *router.Event
	station router.Station
}

// NewProtoAdaptor return new ProtoAdaptor
func NewProtoAdaptor(config *p2p.Config) *ProtoAdaptor {
	adaptor := &ProtoAdaptor{
		Server: p2p.Server{
			Config: config,
		},
		peerMangaer: peerMangaer{
			activePeers: make(map[[8]byte]*remotePeer),
			station:     nil,
		},
		event:   make(chan *router.Event),
		station: router.NewLocalStation("p2p", nil),
	}
	adaptor.peerMangaer.station = router.NewBroadcastStation("broadcast", &adaptor.peerMangaer)
	adaptor.Server.Config.Protocols = adaptor.Protocols()
	return adaptor
}

// Start start p2p protocol adaptor
func (adaptor *ProtoAdaptor) Start() error {
	router.StationRegister(adaptor.peerMangaer.station)
	router.AdaptorRegister(adaptor)
	router.Subscribe(nil, adaptor.event, router.DisconectCtrl, nil)
	go adaptor.adaptorEvent()
	return adaptor.Server.Start()
}

func (adaptor *ProtoAdaptor) adaptorEvent() {
	for {
		e := <-adaptor.event
		switch e.Typecode {
		case router.DisconectCtrl:
			peer := e.Data.(router.Station).Data().(*remotePeer)
			peer.peer.Disconnect(p2p.DiscSubprotocolError)
			//peer.Disconnect(DiscSubprotocolError)
		}
	}
}

func (adaptor *ProtoAdaptor) adaptorLoop(peer *p2p.Peer, ws p2p.MsgReadWriter) error {
	remote := remotePeer{ws: ws, peer: peer}
	log.Info("New remote station", "detail", remote.peer.String())
	router.Println("New remote station", "detail=", remote.peer.String())
	station := router.NewRemoteStation(string(remote.peer.ID().Bytes()[:8]), &remote)
	adaptor.peerMangaer.addActivePeer(&remote)
	router.StationRegister(station)
	url := remote.peer.Node().String()
	router.SendTo(station, nil, router.NewPeerNotify, &url)
	defer func() {
		adaptor.peerMangaer.delActivePeer(&remote)
		router.StationUnregister(station)
		url := remote.peer.Node().String()
		router.SendTo(station, nil, router.DelPeerNotify, &url)
		router.Println("remove remote station", "detail=", remote.peer.String())
		time.Sleep(time.Minute) // delay to prevent the reconnection
	}()

	//monitor := make(map[int][]int64)
	monitor := [router.P2PEndSize]int{}
	timeMonitor := [router.P2PEndSize][3]int64{}
	for {
		msg, err := ws.ReadMsg()
		if err != nil {
			return err
		}
		pack := pack{}
		if err := msg.Decode(&pack); err != nil {
			return err
		}
		e, err := pack2event(&pack, station)
		if err != nil {
			return err
		}
		monitor[e.Typecode]++
		{
			router.Println("monitor=", router.TypeName[e.Typecode], "count=", monitor[e.Typecode], "Txs=", monitor[router.P2PTxMsg])
		}
		/*
			ret := checkDDOS(monitor, e)
			if ret {
				router.SendTo(nil, nil, router.DisconectCtrl, e.From)
				//ToDo blacklist
				return fmt.Errorf("DDos %x", e.From.Name())
			}
		*/
		start := router.TimeMs()
		router.SendEvent(e)
		dur := router.TimeMs() - start
		timeMonitor[e.Typecode][2] += dur
		if timeMonitor[e.Typecode][0] < dur {
			timeMonitor[e.Typecode][0] = dur
		}
		if timeMonitor[e.Typecode][1] > dur || timeMonitor[e.Typecode][1] == 0 {
			timeMonitor[e.Typecode][1] = dur
		}
		{
			router.Println("timeMonitor=", router.TypeName[e.Typecode],
				"max/min/avg=", timeMonitor[e.Typecode][0], timeMonitor[e.Typecode][1], timeMonitor[e.Typecode][2],
				"txs =", timeMonitor[router.P2PTxMsg][0], timeMonitor[router.P2PTxMsg][1], timeMonitor[router.P2PTxMsg][2])
		}
	}
}

func checkDDOS(m map[int][]int64, e *router.Event) bool {
	t := e.Typecode
	limit := int64(router.GetDDosLimit(t))
	if limit == 0 {
		return false
	}

	if len(m[t]) == 0 {
		m[t] = make([]int64, 2)
	}
	//m[t][0] time
	//m[t][1] request per second
	if m[t][0] == time.Now().Unix() {
		m[t][1]++
	} else {
		if m[t][1] > limit {
			return true
		}
		m[t][0] = time.Now().Unix()
		m[t][1] = 1
	}
	return false
}

// Protocols .
func (adaptor *ProtoAdaptor) Protocols() []p2p.Protocol {
	return []p2p.Protocol{
		p2p.Protocol{
			Name:    "FractalTest",
			Version: 1,
			Length:  1,
			Run:     adaptor.adaptorLoop,
		},
	}
}

// Stop .
func (adaptor *ProtoAdaptor) Stop() {
	adaptor.Server.Stop()
	log.Info("P2P networking stopped")
}

// SendOut .
func (adaptor *ProtoAdaptor) SendOut(e *router.Event) error {
	if e.To.IsBroadcast() {
		adaptor.msgBroadcast(e)
		return nil
	}
	return adaptor.msgSend(e)
}

func (adaptor *ProtoAdaptor) msgSend(e *router.Event) error {
	pack, err := event2pack(e)
	if err != nil {
		return err
	}
	return p2p.Send(e.To.Data().(*remotePeer).ws, 0, pack)
}

func (adaptor *ProtoAdaptor) msgBroadcast(e *router.Event) {
	te := *e
	te.To = nil
	te.From = nil
	pack, err := event2pack(&te)
	if err != nil {
		return
	}

	router.Println("msgBroadcast:", router.TypeName[e.Typecode])
	start := router.TimeMs()
	defer func() { router.Println("exit msgBroadcast:", router.TypeName[e.Typecode], router.TimeMs()-start) }()

	send := func(peer *remotePeer) {
		p2p.Send(peer.ws, 0, pack)
	}
	if e.To.Data() != nil {
		pack.To = "" // if sendto 'broadcast' station, remote will broadcast again, and dead loop (-_-)
		e.To.Data().(*peerMangaer).mapActivePeer(send)
		return
	}
	adaptor.peerMangaer.mapActivePeer(send)
}

func event2pack(e *router.Event) (*pack, error) {
	buf, err := rlp.EncodeToBytes(e.Data)
	if err != nil {
		return nil, err
	}
	from := ""
	if e.From != nil {
		from = e.From.Name()
	}
	to := ""
	if e.To != nil {
		to = e.To.Name()[8:]
	}
	return &pack{
		From:     from,
		To:       to,
		Typecode: uint32(e.Typecode),
		Payload:  buf,
	}, nil
}

func pack2event(pack *pack, station router.Station) (*router.Event, error) {
	var elem interface{}

	isPtr := false
	typ := router.GetTypeByCode(int(pack.Typecode))
	if typ == nil {
		return nil, fmt.Errorf("unknow typecode: %d", pack.Typecode)
	}

	//for typ.Kind() == reflect.Ptr {
	if typ.Kind() == reflect.Ptr {
		isPtr = true
		typ = typ.Elem()
	}
	obj := reflect.New(typ)
	if err := rlp.DecodeBytes(pack.Payload, obj.Interface()); err != nil {
		return nil, err
	}
	if isPtr {
		elem = obj.Interface()
	} else {
		elem = obj.Elem().Interface()
	}

	if pack.From != "" {
		station = router.NewRemoteStation(station.Name()+pack.From, station.Data())
	}
	return &router.Event{
		From:     station,
		To:       router.GetStationByName(pack.To),
		Typecode: int(pack.Typecode),
		Data:     elem,
	}, nil
}

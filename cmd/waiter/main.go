package main

import (
	"log"
	"math/rand"
	"time"

	"github.com/sauerbraten/waiter/pkg/maprot"

	"github.com/sauerbraten/jsonfile"
	"github.com/sauerbraten/maitred/pkg/auth"
	mserver "github.com/sauerbraten/maitred/pkg/client"

	"github.com/sauerbraten/waiter/internal/net/enet"
	"github.com/sauerbraten/waiter/internal/relay"
	"github.com/sauerbraten/waiter/pkg/bans"
	"github.com/sauerbraten/waiter/pkg/protocol"
	"github.com/sauerbraten/waiter/pkg/protocol/disconnectreason"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

var (
	// global server state
	s *Server

	// ban manager
	bm *bans.BanManager

	localAuth auth.Provider

	// master server
	ms        *mserver.VanillaClient
	masterInc <-chan string

	// stats server
	statsAuth    *mserver.AdminClient
	statsAuthInc <-chan string

	// info server
	is      *infoServer
	infoInc <-chan infoRequest

	// callbacks (e.g. IP geolocation queries)
	callbacks = make(chan func())
)

func main() {
	var conf *Config
	err := jsonfile.ParseFile("config.json", &conf)
	if err != nil {
		log.Fatalln(err)
	}

	bm, err = bans.FromFile("bans.json")
	if err != nil {
		log.Fatalln(err)
	}

	var users []*auth.User
	err = jsonfile.ParseFile("users.json", &users)
	if err != nil {
		log.Fatalln(err)
	}
	localAuth = auth.NewInMemoryProvider(users)

	host, err := enet.NewHost(conf.ListenAddress, conf.ListenPort)
	if err != nil {
		log.Fatalln(err)
	}

	cs := &ClientManager{}

	s = &Server{
		ENetHost: host,
		Config:   conf,
		State: &State{
			UpSince:    time.Now(),
			NumClients: cs.NumberOfClientsConnected,
		},
		relay:       relay.New(),
		Clients:     cs,
		MapRotation: maprot.NewRotation(conf.MapPools),
		Commands:    NewServerCommands(queueMap, toggleKeepTeams, toggleCompetitiveMode, toggleReportStats, lookupIPs, setTimeLeft, registerPubkey),
	}
	s.GameDurationInMinutes = s.GameDurationInMinutes * time.Minute // duration is parsed without unit from config file
	s.GameMode = NewGame(conf.FallbackGameMode)
	s.Map = s.MapRotation.NextMap(conf.FallbackGameMode, conf.FallbackGameMode, "")
	s.GameMode.Start()
	s.Unsupervised()
	s.Empty()

	is, infoInc = s.StartListeningForInfoRequests()

	ms, masterInc, err = mserver.NewVanilla(conf.MasterServerAddress, conf.ListenPort, bm, role.Auth, func() { s.ReAuth("") })
	if err != nil {
		log.Println("could not connect to master server:", err)
	}

	var _statsAuth *mserver.StatsClient
	_statsAuth, statsAuthInc, err = mserver.NewStats(
		conf.StatsServerAddress,
		conf.ListenPort,
		s.HandleSuccStats,
		s.HandleFailStats,
		func() { s.ReAuth(s.StatsServerAuthDomain) },
	)
	if err != nil {
		log.Println("could not connect to statsauth server:", err)
	}
	statsAuth = mserver.NewAdmin(_statsAuth)

	s.AuthManager = auth.NewManager(map[string]auth.Provider{
		"":                         ms.RemoteProvider,
		conf.ServerAuthDomain:      localAuth,
		conf.StatsServerAuthDomain: statsAuth,
	})

	gameInc := s.ENetHost.Service()

	log.Println("server running on port", s.Config.ListenPort)

	for {
		select {
		case event := <-gameInc:
			handleEnetEvent(event)
		case req := <-infoInc:
			is.Handle(req)
		case msg := <-masterInc:
			go ms.Handle(msg)
		case msg := <-statsAuthInc:
			go statsAuth.Handle(msg)
		case <-time.Tick(1 * time.Hour):
			go ms.Register()
			go statsAuth.Register()
		case f := <-callbacks:
			f()
		}
	}
}

func handleEnetEvent(event enet.Event) {
	switch event.Type {
	case enet.EventTypeConnect:
		s.Connect(event.Peer)

	case enet.EventTypeDisconnect:
		client := s.Clients.GetClientByPeer(event.Peer)
		if client == nil {
			return
		}
		s.Disconnect(client, disconnectreason.None)

	case enet.EventTypeReceive:
		client := s.Clients.GetClientByPeer(event.Peer)
		if client == nil {
			return
		}
		s.handlePacket(client, event.ChannelID, protocol.Packet(event.Packet.Data))
	}
}

package main

import (
	"strconv"

	"log"
	"math/rand"
	"time"

	"github.com/sauerbraten/jsonfile"
	"github.com/sauerbraten/maitred/pkg/auth"
	mserver "github.com/sauerbraten/maitred/pkg/client"

	"github.com/sauerbraten/waiter/pkg/bans"
	"github.com/sauerbraten/waiter/pkg/enet"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
	"github.com/sauerbraten/waiter/pkg/server"
	"github.com/sauerbraten/waiter/pkg/server/info"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

var (
	// ban manager
	bm *bans.BanManager

	localAuth auth.Provider

	// master server
	ms        *mserver.VanillaClient
	masterInc <-chan string

	// stats server
	statsServer    *mserver.AdminClient
	statsServerInc <-chan string

	// info server
	is      *info.InfoServer
	infoInc <-chan info.Request

	// callbacks (e.g. IP geolocation queries)
	callbacks <-chan func()
)

func main() {
	var conf *server.Config
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

	var s *server.Server

	ms, masterInc, err = mserver.NewVanilla(conf.MasterServerAddress, conf.ListenPort, bm, role.Auth, func() { s.ReAuth("") })
	if err != nil {
		log.Println("could not connect to master server:", err)
	}

	var _statsServer *mserver.StatsClient
	_statsServer, statsServerInc, err = mserver.NewStats(
		conf.StatsServerAddress,
		conf.ListenPort,
		s.HandleSuccStats,
		s.HandleFailStats,
		func() { s.ReAuth(conf.StatsServerAuthDomain) },
	)
	if err != nil {
		log.Println("could not connect to statsServer server:", err)
	}
	statsServer = mserver.NewAdmin(_statsServer)

	authManager := auth.NewManager(map[string]auth.Provider{
		"":                         ms.RemoteProvider,
		conf.AuthDomain:            auth.NewInMemoryProvider(users),
		conf.StatsServerAuthDomain: statsServer,
	})

	s, callbacks = server.New(
		*conf,
		authManager,
		server.QueueMap,
		server.ToggleKeepTeams,
		server.ToggleCompetitiveMode,
		server.ToggleReportStats,
		server.LookupIPs,
		server.SetTimeLeft,
		server.RegisterPubkey,
	)

	is, infoInc = info.NewInfoServer(conf.ListenAddress+":"+strconv.Itoa(conf.ListenPort+1), conf.ServerDescription, conf.SendClientIPsViaExtinfo, s)

	host, err := enet.NewHost(conf.ListenAddress, conf.ListenPort)
	if err != nil {
		log.Fatalln(err)
	}

	gameInc := host.Service()

	log.Println("server running on port", conf.ListenPort)

	for {
		select {
		case event := <-gameInc:
			s.HandleENetEvent(event)
		case req := <-infoInc:
			is.Handle(req)
		case msg := <-masterInc:
			go ms.Handle(msg)
		case msg := <-statsServerInc:
			go statsServer.Handle(msg)
		case <-time.Tick(1 * time.Hour):
			go ms.Register()
			go statsServer.Register()
		case f := <-callbacks:
			f()
		}
	}
}

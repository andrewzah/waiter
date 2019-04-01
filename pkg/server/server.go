package server

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/sauerbraten/maitred/pkg/auth"
	mserver "github.com/sauerbraten/maitred/pkg/client"

	"github.com/sauerbraten/waiter/internal/net/packet"
	"github.com/sauerbraten/waiter/internal/relay"
	"github.com/sauerbraten/waiter/pkg/enet"
	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/geoip"
	"github.com/sauerbraten/waiter/pkg/geom"
	"github.com/sauerbraten/waiter/pkg/maprot"
	"github.com/sauerbraten/waiter/pkg/protocol"
	"github.com/sauerbraten/waiter/pkg/protocol/cubecode"
	"github.com/sauerbraten/waiter/pkg/protocol/disconnectreason"
	"github.com/sauerbraten/waiter/pkg/protocol/gamemode"
	"github.com/sauerbraten/waiter/pkg/protocol/mastermode"
	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
	"github.com/sauerbraten/waiter/pkg/protocol/playerstate"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
	"github.com/sauerbraten/waiter/pkg/protocol/weapon"
)

type State struct {
	masterMode mastermode.ID
	gameMode   game.Mode
	mapname    string
}

func (s *State) MasterMode() mastermode.ID { return s.masterMode }

func (s *State) GameMode() game.Mode { return s.gameMode }

func (s *State) Map() string { return s.mapname }

type Server struct {
	State
	config           Config
	clients          *Clients
	relay            *relay.Relay
	authManager      *auth.Manager
	statsServer      *mserver.AdminClient
	mapRotation      *maprot.Rotation
	pendingMapChange *time.Timer
	callbacks        chan<- func()

	// non-standard stuff
	commands        *Commands
	keepTeams       bool
	competitiveMode bool
	reportStats     bool
}

func New(config Config, authManager *auth.Manager, cmds ...*Command) (*Server, <-chan func()) {
	callbacks := make(chan func())

	s := &Server{
		config:      config,
		clients:     &Clients{},
		relay:       relay.New(),
		authManager: authManager,
		mapRotation: maprot.New(config.MapPools),
		callbacks:   callbacks,
	}
	s.commands = NewCommands(s, cmds...)

	s.State = State{
		gameMode: s.newGame(config.FallbackGameMode),
		mapname:  s.mapRotation.NextMap(config.FallbackGameMode, config.FallbackGameMode, ""),
	}

	s.gameMode.Start()
	s.unsupervised()
	s.empty()

	return s, callbacks
}

func (s *Server) HandleENetEvent(event enet.Event) {
	switch event.Type {
	case enet.EventTypeConnect:
		s.Connect(event.Peer)

	case enet.EventTypeDisconnect:
		client := s.clients.clientByPeer(event.Peer)
		if client == nil {
			return
		}
		s.Disconnect(client, disconnectreason.None)

	case enet.EventTypeReceive:
		client := s.clients.clientByPeer(event.Peer)
		if client == nil {
			return
		}
		s.handlePacket(client, event.ChannelID, protocol.Packet(event.Packet.Data))
	}
}

func (s *Server) NumPlayers() int { return s.clients.NumberOfClientsConnected() }

func (s *Server) MaxPlayers() int { return s.config.MaxClients }

func (s *Server) AllPlayers() []game.Player {
	all := []game.Player{}
	s.clients.ForEach(func(c *Client) {
		all = append(all, c.Player)
	})
	return all
}

func (s *Server) Player(cn uint32) game.Player {
	return s.clients.clientByCN(cn).Player
}

func (s *Server) IP(cn uint32) net.IP {
	return s.clients.clientByCN(cn).Peer.Address.IP
}

func (s *Server) Ping(cn uint32) int32 {
	return s.clients.clientByCN(cn).Ping
}

func (s *Server) Role(cn uint32) role.ID {
	return s.clients.clientByCN(cn).Role
}

func (s *Server) GameDuration() time.Duration { return time.Duration(s.config.GameDuration) }

func (s *Server) authRequiredBecause(c *Client) disconnectreason.ID {
	if s.NumPlayers() >= s.MaxPlayers() {
		return disconnectreason.Full
	}
	if s.masterMode >= mastermode.Private {
		return disconnectreason.PrivateMode
	}
	// if ban, ok := bm.GetBan(c.Peer.Address.IP); ok {
	// 	log.Println("connecting client", c, "is banned:", ban)
	// 	return disconnectreason.IPBanned
	// }
	return disconnectreason.None
}

func (s *Server) Connect(peer *enet.Peer) {
	client := s.clients.add(peer)
	client.Positions, client.Packets = s.relay.AddClient(client.CN, client.Peer.Send)
	client.Send(
		nmc.ServerInfo,
		client.CN,
		protocol.Version,
		client.SessionID,
		false, // password protection is not used by this implementation
		s.config.ServerDescription,
		s.config.AuthDomain,
	)
}

// checks the server state and decides wether the client has to authenticate to join the game.
func (s *Server) tryJoin(c *Client, name string, playerModel int32, authDomain, authName string) {
	c.Name = name
	c.Model = playerModel

	onAutoAuthSuccess := func(rol role.ID) {
		s.setAuthRole(c, rol, authDomain, authName)
	}

	onAutoAuthFailure := func(err error) {
		log.Printf("unsuccessful auth try at connect by %s as '%s' [%s]: %v", c, authName, authDomain, err)
	}

	c.AuthRequiredBecause = s.authRequiredBecause(c)

	if c.AuthRequiredBecause == disconnectreason.None {
		s.join(c)
		if authDomain == s.config.AuthDomain && authName != "" {
			go s.handleAuthRequest(c, authDomain, authName, onAutoAuthSuccess, onAutoAuthFailure)
		}
	} else if authDomain == s.config.AuthDomain && authName != "" {
		// not in a new goroutine, so client does not get confused and sends nmc.ClientPing before the player joined
		s.handleAuthRequest(c, authDomain, authName,
			func(rol role.ID) {
				if rol == role.None {
					return
				}
				c.AuthRequiredBecause = disconnectreason.None
				s.join(c)
				onAutoAuthSuccess(rol)
			},
			func(err error) {
				onAutoAuthFailure(err)
				s.Disconnect(c, c.AuthRequiredBecause)
			},
		)
	} else {
		s.Disconnect(c, c.AuthRequiredBecause)
	}
}

// Puts a client into the current game, using the data the client provided with his nmc.TryJoin packet.
func (s *Server) join(c *Client) {
	c.Joined = true

	if s.masterMode == mastermode.Locked {
		c.State = playerstate.Spectator
	} else {
		c.State = playerstate.Dead
		s.Spawn(c)
	}

	s.gameMode.Join(&c.Player)                  // may set client's team
	s.sendWelcome(c)                            // tells client about her team
	typ, initData := s.gameMode.Init(&c.Player) // may send additional welcome info like flags
	if typ != nmc.None {
		c.Send(typ, initData...)
	}
	s.informOthersOfJoin(c)

	sessionID := c.SessionID
	go func() {
		uniqueName := s.clients.UniqueName(c)
		log.Println(cubecode.SanitizeString(fmt.Sprintf("%s (%s) connected", uniqueName, c.Peer.Address.IP)))

		country := geoip.Country(c.Peer.Address.IP) // slow!
		s.callbacks <- func() {
			if c.SessionID != sessionID {
				return
			}
			if country != "" {
				s.clients.Relay(c, nmc.ServerMessage, fmt.Sprintf("%s connected from %s", uniqueName, country))
			}
		}
	}()

	c.Send(nmc.ServerMessage, s.config.MessageOfTheDay)
	c.Send(nmc.RequestAuth, s.config.StatsServerAuthDomain)
}

// Sends 'welcome' information to a newly joined client like map, mode, time left, other players, etc.
func (s *Server) sendWelcome(c *Client) {
	typ, p := nmc.Welcome, []interface{}{
		nmc.MapChange, s.mapname, s.gameMode.ID(), s.gameMode.NeedMapInfo(), // currently played mode & map
	}

	timedMode, timed := s.gameMode.(game.TimedMode)
	if timed {
		p = append(p, nmc.TimeLeft, timedMode.TimeLeft()) // time left in this round
	}

	// send list of clients which have privilege higher than PRIV_NONE and their respecitve privilege level
	pupTyp, pup, empty := s.privilegedUsersPacket()
	if !empty {
		p = append(p, pupTyp, pup)
	}

	if timed && timedMode.Paused() {
		p = append(p, nmc.PauseGame, 1, -1)
	}

	if teamMode, ok := s.gameMode.(game.TeamMode); ok {
		p = append(p, nmc.TeamInfo)
		teamMode.ForEach(func(t *game.Team) {
			if t.Frags > 0 {
				p = append(p, t.Name, t.Frags)
			}
		})
		p = append(p, "")
	}

	// tell the client what team he was put in by the server
	p = append(p, nmc.SetTeam, c.CN, c.Team.Name, -1)

	// tell the client how to spawn (what health, what armour, what weapons, what ammo, etc.)
	if c.State == playerstate.Spectator {
		p = append(p, nmc.Spectator, c.CN, 1)
	} else {
		// TODO: handle spawn delay (e.g. in ctf modes)
		p = append(p, nmc.SpawnState, c.CN, c.ToWire())
	}

	// send other players' state (frags, flags, etc.)
	p = append(p, nmc.Resume)
	s.clients.ForEach(func(_c *Client) {
		if _c != c {
			p = append(p, _c.CN, _c.State, _c.Frags, _c.Flags, _c.QuadTimeLeft, _c.ToWire())
		}
	})
	p = append(p, -1)

	// send other client's state (name, team, playermodel)
	s.clients.ForEach(func(_c *Client) {
		if _c != c {
			p = append(p, nmc.InitializeClient, _c.CN, _c.Name, _c.Team.Name, _c.Model)
		}
	})

	c.Send(typ, p...)
}

// Informs all other clients that a client joined the game.
func (s *Server) informOthersOfJoin(c *Client) {
	s.clients.Relay(c, nmc.InitializeClient, c.CN, c.Name, c.Team.Name, c.Model)
	if c.State == playerstate.Spectator {
		s.clients.Relay(c, nmc.Spectator, c.CN, 1)
	}
}

func (s *Server) privilegedUsersPacket() (typ nmc.ID, p protocol.Packet, noPrivilegedUsers bool) {
	q := []interface{}{s.masterMode}

	s.clients.ForEach(func(c *Client) {
		if c.Role > role.None {
			q = append(q, c.CN, c.Role)
		}
	})

	q = append(q, -1)

	return nmc.CurrentMaster, packet.Encode(q...), len(q) <= 3
}

func (s *Server) Broadcast(typ nmc.ID, args ...interface{}) {
	s.clients.Broadcast(typ, args...)
}

func (s *Server) UniqueName(p *game.Player) string {
	return s.clients.UniqueName(s.clients.clientByCN(p.CN))
}

func (s *Server) Spawn(client *Client) {
	client.Spawn()
	s.gameMode.Spawn(&client.Player)
}

func (s *Server) ConfirmSpawn(client *Client, lifeSequence, _weapon int32) {
	if client.State != playerstate.Dead || lifeSequence != client.LifeSequence || client.LastSpawnAttempt.IsZero() {
		// client may not spawn
		return
	}

	client.State = playerstate.Alive
	client.SelectedWeapon = weapon.ByID(weapon.ID(_weapon))
	client.LastSpawnAttempt = time.Time{}

	client.Packets.Publish(nmc.ConfirmSpawn, client.ToWire())

	s.gameMode.ConfirmSpawn(&client.Player)
}

func (s *Server) Disconnect(client *Client, reason disconnectreason.ID) {
	s.gameMode.Leave(&client.Player)
	s.relay.RemoveClient(client.CN)
	s.clients.Disconnect(client, reason)
	//s.ENetHost.Disconnect(client.Peer, reason)
	client.Reset()
	if len(s.clients.PrivilegedUsers()) == 0 {
		s.unsupervised()
	}
	if s.clients.NumberOfClientsConnected() == 0 {
		s.empty()
	}
}

func (s *Server) kick(client *Client, victim *Client, reason string) {
	if client.Role <= victim.Role {
		client.Send(nmc.ServerMessage, cubecode.Fail("you can't do that"))
		return
	}
	msg := fmt.Sprintf("%s kicked %s", s.clients.UniqueName(client), s.clients.UniqueName(victim))
	if reason != "" {
		msg += " for: " + reason
	}
	s.clients.Broadcast(nmc.ServerMessage, msg)
	s.Disconnect(victim, disconnectreason.Kick)
}

func (s *Server) authKick(client *Client, rol role.ID, domain, name string, victim *Client, reason string) {
	if rol <= victim.Role {
		client.Send(nmc.ServerMessage, cubecode.Fail("you can't do that"))
		return
	}
	msg := fmt.Sprintf("%s as '%s' [%s] kicked %s", s.clients.UniqueName(client), cubecode.Magenta(name), cubecode.Green(domain), s.clients.UniqueName(victim))
	if reason != "" {
		msg += " for: " + reason
	}
	s.clients.Broadcast(nmc.ServerMessage, msg)
	s.Disconnect(victim, disconnectreason.Kick)
}

func (s *Server) unsupervised() {
	timedMode, isTimedMode := s.gameMode.(game.TimedMode)
	if isTimedMode {
		timedMode.Resume(nil)
	}
	s.masterMode = mastermode.Open
	s.keepTeams = false
	s.competitiveMode = false
	s.reportStats = true
}

func (s *Server) empty() {
	s.mapRotation.ClearQueue()
	if s.gameMode.ID() != s.config.FallbackGameMode {
		s.changeMap(s.config.FallbackGameMode, s.mapRotation.NextMap(s.config.FallbackGameMode, s.gameMode.ID(), s.mapname))
	}
}

func (s *Server) Intermission() {
	s.gameMode.End()

	nextMap := s.mapRotation.NextMap(s.gameMode.ID(), s.gameMode.ID(), s.mapname)

	s.pendingMapChange = time.AfterFunc(10*time.Second, func() {
		s.changeMap(s.gameMode.ID(), nextMap)
	})

	s.clients.Broadcast(nmc.ServerMessage, "next up: "+nextMap)

	if s.reportStats && s.NumPlayers() > 0 && s.statsServer != nil {
		s.reportEndgameStats()
	}
}

func (s *Server) reportEndgameStats() {
	stats := []string{}
	s.clients.ForEach(func(c *Client) {
		if a, ok := c.Authentications[s.config.StatsServerAuthDomain]; ok {
			stats = append(stats, fmt.Sprintf("%d %s %d %d %d %d %d", a.reqID, a.name, c.Frags, c.Deaths, c.Damage, c.DamagePotential, c.Flags))
		}
	})

	s.statsServer.Send("stats %d %s %s", s.gameMode.ID(), s.mapname, strings.Join(stats, " "))
}

func (s *Server) HandleSuccStats(reqID uint32) {
	s.clients.ForEach(func(c *Client) {
		if a, ok := c.Authentications[s.config.StatsServerAuthDomain]; ok && a.reqID == reqID {
			c.Send(nmc.ServerMessage, fmt.Sprintf("your game statistics were reported to %s", s.config.StatsServerAuthDomain))
		}
	})
}

func (s *Server) HandleFailStats(reqID uint32, reason string) {
	s.clients.ForEach(func(c *Client) {
		if a, ok := c.Authentications[s.config.StatsServerAuthDomain]; ok && a.reqID == reqID {
			c.Send(nmc.ServerMessage, fmt.Sprintf("reporting your game statistics failed: %s", reason))
		}
	})
}

func (s *Server) ReAuth(domain string) {
	s.clients.ForEach(func(c *Client) {
		if _, ok := c.Authentications[domain]; ok {
			delete(c.Authentications, domain)
			c.Send(nmc.RequestAuth, domain)
		}
	})
}

func (s *Server) changeMap(mode gamemode.ID, mapname string) {
	// cancel pending timers
	if s.gameMode != nil {
		s.gameMode.CleanUp()
	}

	// stop any pending map change
	if s.pendingMapChange != nil {
		s.pendingMapChange.Stop()
	}

	s.mapname = mapname
	s.gameMode = s.newGame(mode)

	s.ForEach(s.gameMode.Join)
	s.clients.Broadcast(nmc.MapChange, s.mapname, s.gameMode.ID(), s.gameMode.NeedMapInfo())
	s.gameMode.Start()

	s.clients.ForEach(func(c *Client) {
		c.Player.PlayerState.Reset()
		if c.State == playerstate.Spectator {
			return
		}
		s.Spawn(c)
		c.Send(nmc.SpawnState, c.CN, c.ToWire())
	})

	s.clients.Broadcast(nmc.ServerMessage, s.config.MessageOfTheDay)
}

func (s *Server) newGame(id gamemode.ID) game.Mode {
	mode := func() game.Mode {
		switch id {
		case gamemode.Insta:
			return game.NewInsta(s)
		case gamemode.InstaTeam:
			return game.NewInstaTeam(s, s.keepTeams)
		case gamemode.Effic:
			return game.NewEffic(s)
		case gamemode.EfficTeam:
			return game.NewEfficTeam(s, s.keepTeams)
		case gamemode.Tactics:
			return game.NewTactics(s)
		case gamemode.TacticsTeam:
			return game.NewTacticsTeam(s, s.keepTeams)
		case gamemode.InstaCTF:
			return game.NewInstaCTF(s, s.keepTeams)
		case gamemode.EfficCTF:
			return game.NewEfficCTF(s, s.keepTeams)
		default:
			return nil
		}
	}()

	if timed, ok := mode.(game.TimedMode); ok && s.competitiveMode {
		return game.NewCompetitive(s, timed)
	}
	return mode
}

func (s *Server) setMasterMode(c *Client, mm mastermode.ID) {
	if mm < mastermode.Open || mm > mastermode.Private {
		log.Println("invalid mastermode", mm, "requested")
		return
	}
	if c.Role == role.None {
		c.Send(nmc.ServerMessage, cubecode.Fail("you can't do that"))
		return
	}
	s.masterMode = mm
	s.clients.Broadcast(nmc.MasterMode, mm)
}

type hit struct {
	target       uint32
	lifeSequence int32
	distance     float64
	rays         int32
	dir          *geom.Vector
}

func (s *Server) handleShoot(client *Client, wpn weapon.Weapon, id int32, from, to *geom.Vector, hits []hit) {
	from = from.Mul(geom.DMF)
	to = to.Mul(geom.DMF)

	s.clients.Relay(
		client,
		nmc.ShotEffects,
		client.CN,
		wpn.ID,
		id,
		from.X(),
		from.Y(),
		from.Z(),
		to.X(),
		to.Y(),
		to.Z(),
	)
	client.LastShot = time.Now()
	client.DamagePotential += wpn.Damage * wpn.Rays // TODO: quad damage
	if wpn.ID != weapon.Saw {
		client.Ammo[wpn.ID]--
	}
	switch wpn.ID {
	case weapon.GrenadeLauncher, weapon.RocketLauncher:
		// wait for nmc.Explode pkg
	default:
		// apply damage
		rays := int32(0)
		for _, h := range hits {
			target := s.clients.clientByCN(h.target)
			if target == nil ||
				target.State != playerstate.Alive ||
				target.LifeSequence != h.lifeSequence ||
				h.rays < 1 ||
				h.distance > wpn.Range+1.0 {
				continue
			}

			rays += h.rays
			if rays > wpn.Rays {
				continue
			}

			damage := h.rays * wpn.Damage
			// TODO: quad damage

			s.applyDamage(client, target, int32(damage), wpn.ID, h.dir)
		}
	}
}

func (s *Server) handleExplode(client *Client, millis int32, wpn weapon.Weapon, id int32, hits []hit) {
	// TODO: delete stored projectile

	s.clients.Relay(
		client,
		nmc.ExplodeEffects,
		client.CN,
		wpn.ID,
		id,
	)

	// apply damage
hits:
	for i, h := range hits {
		target := s.clients.clientByCN(h.target)
		if target == nil ||
			target.State != playerstate.Alive ||
			target.LifeSequence != h.lifeSequence ||
			h.distance < 0 ||
			h.distance > wpn.ExplosionRadius {
			continue
		}

		// avoid duplicates
		for j := range hits[:i] {
			if hits[j].target == h.target {
				continue hits
			}
		}

		damage := float64(wpn.Damage)
		// TODO: quad damage
		damage *= (1 - h.distance/weapon.ExplosionDistanceScale/wpn.ExplosionRadius)
		if target == client {
			damage *= weapon.ExplosionSelfDamageScale
		}

		s.applyDamage(client, target, int32(damage), wpn.ID, h.dir)
	}
}

func (s *Server) applyDamage(attacker, victim *Client, damage int32, wpnID weapon.ID, dir *geom.Vector) {
	victim.ApplyDamage(&attacker.Player, damage, wpnID, dir)
	s.clients.Broadcast(nmc.Damage, victim.CN, attacker.CN, damage, victim.Armour, victim.Health)
	// TODO: setpushed ???
	if !dir.IsZero() {
		dir = dir.Scale(geom.DNF)
		typ, p := nmc.HitPush, []interface{}{victim.CN, wpnID, damage, dir.X(), dir.Y(), dir.Z()}
		if victim.Health <= 0 {
			s.clients.Broadcast(typ, p...)
		} else {
			victim.Send(typ, p...)
		}
	}
	if victim.Health <= 0 {
		s.gameMode.HandleFrag(&attacker.Player, &victim.Player)
	}
}

func (s *Server) ForEach(f func(p *game.Player)) {
	s.clients.ForEach(func(c *Client) {
		f(&c.Player)
	})
}

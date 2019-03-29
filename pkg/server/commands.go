package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/protocol/cubecode"
	"github.com/sauerbraten/waiter/pkg/protocol/mastermode"
	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
)

type Command struct {
	name       string
	aliases    []string
	argsFormat string
	minRole    role.ID
	f          func(s *Server, c *Client, args []string)
}

func (cmd *Command) String() string {
	return fmt.Sprintf("%s %s (aka %s)", cubecode.Green(cmd.name), cmd.argsFormat, strings.Join(cmd.aliases, ", "))
}

type Commands struct {
	s       *Server
	byAlias map[string]*Command
	cmds    []*Command
}

func NewCommands(s *Server, cmds ...*Command) *Commands {
	sc := &Commands{
		s:       s,
		byAlias: map[string]*Command{},
	}
	for _, cmd := range cmds {
		sc.Register(cmd)
	}
	return sc
}

func (sc *Commands) Register(cmd *Command) {
	sc.byAlias[cmd.name] = cmd
	for _, alias := range cmd.aliases {
		sc.byAlias[alias] = cmd
	}
	sc.cmds = append(sc.cmds, cmd)
}

func (sc *Commands) Help(c *Client) {
	helpLines := []string{}
	for _, cmd := range sc.cmds {
		if c.Role >= cmd.minRole {
			helpLines = append(helpLines, cmd.String())
		}
	}
	c.Send(nmc.ServerMessage, "available commands: "+strings.Join(helpLines, ", "))
}

func (sc *Commands) Handle(c *Client, msg string) {
	parts := strings.Split(strings.TrimSpace(msg), " ")
	commandName, args := parts[0], parts[1:]

	switch commandName {
	case "help", "commands":
		sc.Help(c)

	default:
		cmd, ok := sc.byAlias[commandName]
		if !ok {
			c.Send(nmc.ServerMessage, cubecode.Fail("unknown command "+commandName))
			return
		}

		if c.Role < cmd.minRole {
			return
		}

		cmd.f(sc.s, c, args)
	}
}

var queueMap = &Command{
	name:       "queue",
	aliases:    []string{"queued", "queuemap", "queuedmap", "queuemaps", "queuedmaps", "mapqueue", "mapsqueue"},
	argsFormat: "[map...]",
	minRole:    role.Master,
	f: func(s *Server, c *Client, args []string) {
		for _, mapname := range args {
			err := s.mapRotation.QueueMap(s.gameMode.ID(), mapname)
			if err != "" {
				c.Send(nmc.ServerMessage, cubecode.Fail(err))
			}
		}
		queuedMaps := s.mapRotation.QueuedMaps()
		switch len(queuedMaps) {
		case 0:
			c.Send(nmc.ServerMessage, "no maps queued")
		case 1:
			c.Send(nmc.ServerMessage, "queued map: "+queuedMaps[0])
		default:
			c.Send(nmc.ServerMessage, "queued maps: "+strings.Join(queuedMaps, ", "))
		}
	},
}

var toggleKeepTeams = &Command{
	name:       "keepteams",
	aliases:    []string{"persist", "persistteams"},
	argsFormat: "0|1",
	minRole:    role.Master,
	f: func(s *Server, c *Client, args []string) {
		changed := false
		if len(args) >= 1 {
			val, err := strconv.Atoi(args[0])
			if err != nil || (val != 0 && val != 1) {
				return
			}
			changed = s.keepTeams != (val == 1)
			s.keepTeams = val == 1
		}
		if changed {
			if s.keepTeams {
				s.clients.Broadcast(nmc.ServerMessage, "teams will be kept")
			} else {
				s.clients.Broadcast(nmc.ServerMessage, "teams will be shuffled")
			}
		} else {
			if s.keepTeams {
				c.Send(nmc.ServerMessage, "teams will be kept")
			} else {
				c.Send(nmc.ServerMessage, "teams will be shuffled")
			}
		}
	},
}

var toggleCompetitiveMode = &Command{
	name:       "comp",
	aliases:    []string{"competitive"},
	argsFormat: "0|1",
	minRole:    role.Master,
	f: func(s *Server, c *Client, args []string) {
		changed := false
		if len(args) >= 1 {
			val, err := strconv.Atoi(args[0])
			if err != nil || (val != 0 && val != 1) {
				return
			}
			comp, active := s.gameMode.(*game.Competitive)
			changed = s.competitiveMode != (val == 1)
			switch val {
			case 1:
				// starts at next map
				s.competitiveMode = true
				// but lock server now
				s.setMasterMode(c, mastermode.Locked)
			default:
				if active {
					// stops immediately
					s.gameMode = comp.ToCasual()
					s.competitiveMode = false
				}
			}
		}
		if changed {
			if s.competitiveMode {
				s.clients.Broadcast(nmc.ServerMessage, "competitive mode will be enabled with next game")
			} else {
				s.clients.Broadcast(nmc.ServerMessage, "competitive mode disabled")
			}
		} else {
			if s.competitiveMode {
				c.Send(nmc.ServerMessage, "competitive mode is on")
			} else {
				c.Send(nmc.ServerMessage, "competitive mode is off")
			}
		}
	},
}

var toggleReportStats = &Command{
	name:       "repstats",
	aliases:    []string{"reportstats"},
	argsFormat: "0|1",
	minRole:    role.Admin,
	f: func(s *Server, c *Client, args []string) {
		changed := false
		if len(args) >= 1 {
			val, err := strconv.Atoi(args[0])
			if err != nil || (val != 0 && val != 1) {
				return
			}
			changed = s.reportStats != (val == 1)
			s.reportStats = val == 1
		}
		if changed {
			if s.reportStats {
				s.clients.Broadcast(nmc.ServerMessage, "stats will be reported at intermission")
			} else {
				s.clients.Broadcast(nmc.ServerMessage, "stats will not be reported")
			}
		} else {
			if s.reportStats {
				c.Send(nmc.ServerMessage, "stats reporting is on")
			} else {
				c.Send(nmc.ServerMessage, "stats reporting is off")
			}
		}
	},
}

var lookupIPs = &Command{
	name:       "ip",
	aliases:    []string{"ips"},
	argsFormat: "<name|cn>...",
	minRole:    role.Admin,
	f: func(s *Server, c *Client, args []string) {
		if len(args) < 1 {
			return
		}
		for _, query := range args {
			var target *Client
			// try CN
			cn, err := strconv.Atoi(query)
			if err == nil {
				target = s.clients.clientByCN(uint32(cn))
			}
			if err != nil || target == nil {
				target = s.clients.findClientByName(query)
			}

			if target != nil {
				c.Send(nmc.ServerMessage, fmt.Sprintf("%s has IP %s", s.clients.UniqueName(target), target.Peer.Address.IP))
			} else {
				c.Send(nmc.ServerMessage, fmt.Sprintf("could not find a client matching '%s'", query))
			}
		}
	},
}

var setTimeLeft = &Command{
	name:       "time",
	aliases:    []string{"settime", "settimeleft", "settimeremaining", "timeleft", "timeremaining"},
	argsFormat: "[Xm]Ys",
	minRole:    role.Admin,
	f: func(s *Server, c *Client, args []string) {
		if len(args) < 1 {
			return
		}

		timedMode, isTimedMode := s.gameMode.(game.TimedMode)
		if !isTimedMode {
			c.Send(nmc.ServerMessage, cubecode.Fail("not running a timed mode"))
			return
		}

		d, err := time.ParseDuration(args[0])
		if err != nil {
			c.Send(nmc.ServerMessage, cubecode.Error("could not parse duration: "+err.Error()))
			return
		}

		if d == 0 {
			d = 1 * time.Second // 0 forces intermission without updating the client's game timer
			s.clients.Broadcast(nmc.ServerMessage, cubecode.Orange(fmt.Sprintf("%s forced intermission", s.clients.UniqueName(c))))
		} else {
			s.clients.Broadcast(nmc.ServerMessage, cubecode.Orange(fmt.Sprintf("%s set the time remaining to %s", s.clients.UniqueName(c), d)))
		}

		timedMode.SetTimeLeft(d)
	},
}

var registerPubkey = &Command{
	name:       "register",
	aliases:    []string{},
	argsFormat: "[name] <pubkey>",
	minRole:    role.None,
	f: func(s *Server, c *Client, args []string) {
		if statsAuth, ok := c.Authentications[s.config.StatsServerAuthDomain]; ok {
			c.Send(nmc.ServerMessage, cubecode.Fail("you're already authenticated with "+s.config.StatsServerAuthDomain+" as "+statsAuth.name))
			return
		}

		if len(args) < 1 {
			c.Send(nmc.ServerMessage, cubecode.Fail("you have to include a public key: /servcmd register [name] <pubkey>"))
			return
		}

		name, pubkey := "", ""
		if len(args) < 2 {
			gauth, ok := c.Authentications[""]
			if !ok {
				c.Send(nmc.ServerMessage, cubecode.Fail("you have to claim gauth (to use your gauth name) or provide a name: /servcmd register [name] <pubkey>"))
				return
			}
			name, pubkey = gauth.name, args[0]
		} else {
			name, pubkey = args[0], args[1]
		}

		if pubkey == "" {
			c.Send(nmc.ServerMessage, cubecode.Fail("you have to provide your public key: /servcmd register [name] <pubkey>"))
			return
		}

		s.statsServer.AddAuth(name, pubkey,
			func(err string) {
				if err != "" {
					c.Send(nmc.ServerMessage, cubecode.Error("creating your account failed: "+err))
					return
				}
				c.Send(nmc.ServerMessage, cubecode.Green("you successfully registered as "+name))
				c.Send(nmc.ServerMessage, cubecode.Fail("this is alpha functionality, the account will be lost at stats server restart!"))
				c.Send(nmc.ServerMessage, "type '/autoauth 1', then '/reconnect' to try out your new key")
			},
		)
	},
}

package main

import (
	"log"
	"time"

	"github.com/ivahaev/timer"

	"github.com/sauerbraten/waiter/internal/definitions/gamemode"
	"github.com/sauerbraten/waiter/internal/definitions/nmc"
	"github.com/sauerbraten/waiter/internal/definitions/playerstate"
	"github.com/sauerbraten/waiter/internal/geom"
	"github.com/sauerbraten/waiter/pkg/protocol"
)

type CTFMode interface {
	TeamMode
	IsCTF() // used as marker for ctf modes
}

type flag struct {
	id            int32
	team          int32
	owner         *Client
	version       int32
	dropTime      time.Time
	dropLocation  *geom.Vector
	spawnLocation *geom.Vector
	pendingReset  *timer.Timer
}

type flagMode struct {
	flagsInitialized bool
}

type ctfMode struct {
	timedMode
	teamMode
	flagMode
	good flag
	evil flag
}

func newCTFMode(duration time.Duration, keepTeams bool, intermission func()) ctfMode {
	return ctfMode{
		timedMode: newTimedMode(duration, intermission),
		teamMode:  newTeamMode(false, keepTeams, "good", "evil"),
	}
}

// implement CTFMode interface
func (*ctfMode) IsCTF() {}

func (ctf *ctfMode) Pause(c *Client) {
	if ctf.good.pendingReset != nil {
		ctf.good.pendingReset.Pause()
	}
	if ctf.evil.pendingReset != nil {
		ctf.evil.pendingReset.Pause()
	}
	ctf.timedMode.Pause(c)
}

func (ctf *ctfMode) Resume(c *Client) {
	if ctf.good.pendingReset != nil {
		ctf.good.pendingReset.Start()
	}
	if ctf.evil.pendingReset != nil {
		ctf.evil.pendingReset.Start()
	}
	ctf.timedMode.Resume(c)
}

func (ctf *ctfMode) flagByID(id int32) (*flag, bool) {
	switch id {
	case 0:
		return &ctf.good, true
	case 1:
		return &ctf.evil, true
	default:
		return nil, false
	}
}

func (ctf *ctfMode) teamByFlag(f *flag) string {
	switch f.team {
	case 1:
		return "good"
	case 2:
		return "evil"
	default:
		return ""
	}
}

func (ctf *ctfMode) HandlePacket(client *Client, packetType nmc.ID, p *protocol.Packet) bool {
	switch packetType {
	case nmc.InitFlags:
		ctf.initFlags(ctf.parseFlags(p))

	case nmc.TakeFlag:
		id, ok := p.GetInt()
		if !ok {
			log.Println("could not read flag ID from takeflag packet (packet too short):", p)
			break
		}
		version, ok := p.GetInt()
		if !ok {
			log.Println("could not read flag version from takeflag packet (packet too short):", p)
			break
		}
		ctf.touchFlag(client, id, version)

	case nmc.TryDropFlag:
		ctf.DropFlag(client)

	default:
		return false
	}

	return true
}

func (*ctfMode) parseFlags(p *protocol.Packet) (f1, f2 *flag) {
	numFlags, ok := p.GetInt()
	if !ok {
		log.Println("could not read number of flags from initflags packet (packet too short):", p)
		return
	}
	if numFlags != 2 {
		log.Println("received", numFlags, "flags in CTF mode")
		return
	}

	f1, f2 = &flag{}, &flag{}
	for id, flag := range []*flag{f1, f2} {
		flag.id = int32(id)

		flag.team, ok = p.GetInt()
		if !ok {
			log.Println("could not read flag team from initflags packet (packet too short):", p)
			return
		}

		flag.spawnLocation, ok = parseVector(p)
		if !ok {
			log.Println("could not read flag spawn location from initflags packet (packet too short):", p)
			return
		}
		flag.spawnLocation = flag.spawnLocation.Mul(1 / geom.DMF)
	}

	return
}

func (ctf *ctfMode) initFlags(f1, f2 *flag) {
	if ctf.flagsInitialized || f1 == nil || f2 == nil {
		return
	}

	for _, f := range []*flag{f1, f2} {
		flag, ok := ctf.flagByID(f.id)
		if !ok {
			log.Printf("received invalid flag ID '%d' in CTF mode", f.id)
			continue
		}

		*flag = *f
	}

	ctf.flagsInitialized = true
}

func (ctf *ctfMode) touchFlag(client *Client, id int32, version int32) {
	if !ctf.flagsInitialized {
		return
	}

	flag, ok := ctf.flagByID(id)
	if !ok {
		log.Printf("received invalid flag id '%d' in CTF mode", id)
		return
	}

	if flag.owner != nil || flag.version != version || client.GameState.State != playerstate.Alive {
		return
	}

	team := ctf.teamByFlag(flag)

	if client.Team.Name != team {
		// player stealing enemy flag
		ctf.takeFlag(client, flag)
	} else if !flag.dropTime.IsZero() {
		// player touches her own, dropped flag
		ctf.returnFlag(flag)
		flag.version++
		s.Clients.Broadcast(nil, nmc.ReturnFlag, client.CN, flag.id, flag.version)
		return
	} else {
		// player touches her own, spawned flag
		enemyFlag, ok := ctf.flagByID(1 - id)
		if !ok {
			log.Println("could not get other flag in CTF mode")
			return
		}

		if enemyFlag.owner == client {
			ctf.returnFlag(enemyFlag)
			client.GameState.Flags++
			ctf.teams[team].Score++
			flag.version++
			enemyFlag.version++
			s.Clients.Broadcast(nil, nmc.ScoreFlag, client.CN, enemyFlag.id, enemyFlag.version, flag.id, flag.version, 0, flag.team, ctf.teams[team].Score, client.GameState.Flags)
			if ctf.teams[team].Score >= 10 {
				s.Intermission()
			}
		}
	}
}

func (ctf *ctfMode) takeFlag(client *Client, f *flag) {
	// cancel reset
	if f.pendingReset != nil {
		f.pendingReset.Stop()
		f.pendingReset = nil
	}

	f.version++
	s.Clients.Broadcast(nil, nmc.TakeFlag, client.CN, f.id, f.version)
	f.owner = client
}

func (ctf *ctfMode) DropFlag(client *Client) {
	if !ctf.flagsInitialized {
		return
	}

	var f *flag
	switch client.Team.Name {
	case "good":
		f = &ctf.evil
	case "evil":
		f = &ctf.good
	default:
		return
	}

	if f.owner != client {
		return
	}

	f.dropLocation = client.CurrentPos
	f.dropTime = time.Now()
	f.owner = nil
	f.version++

	s.Clients.Broadcast(nil, nmc.DropFlag, client.CN, f.id, f.version, f.dropLocation.Mul(geom.DMF))
	f.pendingReset = timer.AfterFunc(10*time.Second, func() {
		ctf.returnFlag(f)
		f.version++
		s.Clients.Broadcast(nil, nmc.ResetFlag, f.id, f.version, 0, f.team, ctf.teams[ctf.teamByFlag(f)].Score)
	})
	f.pendingReset.Start()
}

func (ctf *ctfMode) returnFlag(f *flag) {
	f.dropTime = time.Time{}
	f.owner = nil
}

func (ctf *ctfMode) NeedMapInfo() bool { return !ctf.flagsInitialized }

func (ctf *ctfMode) Init(client *Client) {
	q := []interface{}{
		nmc.InitFlags,
		ctf.teams["good"].Score,
		ctf.teams["evil"].Score,
	}

	if ctf.flagsInitialized {
		q = append(q, 2)
		for _, f := range []flag{ctf.good, ctf.evil} {
			var ownerCN int32 = -1
			if f.owner != nil {
				ownerCN = int32(f.owner.CN)
			}
			q = append(q, f.version, 0, ownerCN, 0)
			if f.owner == nil {
				dropped := !f.dropTime.IsZero()
				q = append(q, dropped)
				if dropped {
					q = append(q, f.dropLocation.Mul(geom.DMF))
				}
			}
		}
	} else {
		q = append(q, 0)
	}

	client.Send(q...)
}

func (ctf *ctfMode) Leave(client *Client) {
	ctf.DropFlag(client)
	ctf.teamMode.Leave(client)
}

func (ctf *ctfMode) CanSpawn(c *Client) bool {
	return c.GameState.LastDeath.IsZero() || time.Since(c.GameState.LastDeath) > 5*time.Second
}

func (ctf *ctfMode) HandleDeath(_, victim *Client) {
	ctf.DropFlag(victim)
}

func (ctf *ctfMode) Intermission() {
	// todo: print most flag scores/returns/steals
}

func (ctf *ctfMode) CleanUp() {
	if ctf.good.pendingReset != nil {
		ctf.good.pendingReset.Stop()
	}
	if ctf.evil.pendingReset != nil {
		ctf.evil.pendingReset.Stop()
	}
}

type EfficCTF struct {
	casualMode
	efficMode
	ctfMode
}

// assert interface implementations at compile time
var (
	_ GameMode = NewEfficCTF(1*time.Minute, false)
	_ TeamMode = NewEfficCTF(1*time.Minute, false)
	_ CTFMode  = NewEfficCTF(1*time.Minute, false)
)

func NewEfficCTF(duration time.Duration, keepTeams bool) *EfficCTF {
	var ectf *EfficCTF
	ectf = &EfficCTF{
		ctfMode: newCTFMode(duration, keepTeams, func() { ectf.Intermission() }),
	}
	return ectf
}

func (*EfficCTF) ID() gamemode.ID { return gamemode.EfficCTF }

type InstaCTF struct {
	casualMode
	instaMode
	ctfMode
}

// assert interface implementations at compile time
var (
	_ GameMode = NewInstaCTF(1*time.Minute, false)
	_ TeamMode = NewInstaCTF(1*time.Minute, false)
	_ CTFMode  = NewInstaCTF(1*time.Minute, false)
)

func NewInstaCTF(duration time.Duration, keepTeams bool) *InstaCTF {
	var ictf *InstaCTF
	ictf = &InstaCTF{
		ctfMode: newCTFMode(duration, keepTeams, func() { ictf.Intermission() }),
	}
	return ictf
}

func (*InstaCTF) ID() gamemode.ID { return gamemode.InstaCTF }

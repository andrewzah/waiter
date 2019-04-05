package game

import (
	"fmt"
	"time"

	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
	"github.com/sauerbraten/waiter/pkg/protocol/playerstate"
)

type CompetitiveMode interface {
	Competitive
	Mode
}

type Competitive interface {
	ConfirmSpawn(*Player)
	ToCasual() TimedMode
}

type competitiveMode struct {
	competitivelyTimed
	Mode
}

var (
	_ Mode            = &competitiveMode{}
	_ TimedMode       = &competitiveMode{}
	_ CompetitiveMode = &competitiveMode{}
)

func NewCompetitiveMode(s Server, m func(Timed) TimedMode) CompetitiveMode {
	t := newCompetitivelyTimed(s)
	return &competitiveMode{
		competitivelyTimed: t,
		Mode:               m(&t),
	}
}

func (c *competitiveMode) ConfirmSpawn(p *Player) {
	if _, ok := c.mapLoadPending[p]; ok {
		delete(c.mapLoadPending, p)
		if len(c.mapLoadPending) == 0 {
			c.s.Broadcast(nmc.ServerMessage, "all players spawned, starting game")
			c.Resume(nil)
		}
	}
}

func (c *competitiveMode) ToCasual() TimedMode {
	return struct {
		Timed
		Mode
	}{
		Timed: c.competitivelyTimed.Timed,
		Mode:  c.Mode,
	}
}

func (c *competitiveMode) Leave(p *Player) {
	c.Mode.Leave(p)
	if p.State != playerstate.Spectator && !c.competitivelyTimed.Ended() {
		c.s.Broadcast(nmc.ServerMessage, "a player left the game")
		if !c.Paused() {
			c.Pause(nil)
		} else if len(c.pendingResumeActions) > 0 {
			// a resume is pending, cancel it
			c.Resume(nil)
		}
	}
}

type competitivelyTimed struct {
	s Server
	Timed
	started              bool
	mapLoadPending       map[*Player]struct{}
	pendingResumeActions []*time.Timer
}

var _ Timed = &competitivelyTimed{}

func newCompetitivelyTimed(s Server) competitivelyTimed {
	return competitivelyTimed{
		s:              s,
		Timed:          NewTimedMode(s),
		mapLoadPending: map[*Player]struct{}{},
	}
}

func (c *competitivelyTimed) Start() {
	c.Timed.Start()
	c.s.ForEach(func(p *Player) {
		if p.State != playerstate.Spectator {
			c.mapLoadPending[p] = struct{}{}
		}
	})
	if len(c.mapLoadPending) > 0 {
		c.s.Broadcast(nmc.ServerMessage, "waiting for all players to load the map")
		c.Pause(nil)
	}
}

func (c *competitivelyTimed) Resume(p *Player) {
	if len(c.pendingResumeActions) > 0 {
		for _, action := range c.pendingResumeActions {
			if action != nil {
				action.Stop()
			}
		}
		c.pendingResumeActions = nil
		c.s.Broadcast(nmc.ServerMessage, "resuming aborted")
		return
	}

	if p != nil {
		c.s.Broadcast(nmc.ServerMessage, fmt.Sprintf("%s wants to resume the game", c.s.UniqueName(p)))
	}
	c.s.Broadcast(nmc.ServerMessage, "resuming game in 3 seconds")
	c.pendingResumeActions = []*time.Timer{
		time.AfterFunc(1*time.Second, func() { c.s.Broadcast(nmc.ServerMessage, "resuming game in 2 seconds") }),
		time.AfterFunc(2*time.Second, func() { c.s.Broadcast(nmc.ServerMessage, "resuming game in 1 second") }),
		time.AfterFunc(3*time.Second, func() {
			c.Timed.Resume(p)
			c.pendingResumeActions = nil
		}),
	}
}

func (c *competitivelyTimed) CleanUp() {
	if len(c.pendingResumeActions) > 0 {
		for _, action := range c.pendingResumeActions {
			if action != nil {
				action.Stop()
			}
		}
		c.pendingResumeActions = nil
	}
	c.Timed.CleanUp()
}

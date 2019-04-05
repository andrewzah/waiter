package game

import (
	"time"

	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
)

type TimedMode interface {
	Timed
	Mode
}

type Timed interface {
	Start()
	Pause(*Player)
	Paused() bool
	Resume(*Player)
	End()
	Ended() bool
	CleanUp()
	TimeLeft() time.Duration
	SetTimeLeft(time.Duration)
}

type timed struct {
	t *Timer
	s Server
}

var _ Timed = &timed{}

func NewTimedMode(s Server) Timed {
	return &timed{
		s: s,
	}
}

func (tm *timed) Start() {
	tm.t = StartTimer(tm.s.GameDuration(), tm.s.Intermission)
	tm.s.Broadcast(nmc.TimeLeft, tm.s.GameDuration())
}

func (tm *timed) Pause(p *Player) {
	cn := -1
	if p != nil {
		cn = int(p.CN)
	}
	tm.s.Broadcast(nmc.PauseGame, 1, cn)
	tm.t.Pause()
}

func (tm *timed) Paused() bool {
	return tm.t.Paused()
}

func (tm *timed) Resume(p *Player) {
	cn := -1
	if p != nil {
		cn = int(p.CN)
	}
	tm.s.Broadcast(nmc.PauseGame, 0, cn)
	tm.t.Resume()
}

func (tm *timed) End() {
	tm.s.Broadcast(nmc.TimeLeft, 0)
	tm.t.Stop()
}

func (tm *timed) Ended() bool {
	return tm.t.Stopped()
}

func (tm *timed) CleanUp() {
	if tm.Paused() {
		tm.Resume(nil)
	}
	tm.t.Stop()
}

func (tm *timed) TimeLeft() time.Duration {
	return tm.t.TimeLeft
}

func (tm *timed) SetTimeLeft(d time.Duration) {
	tm.t.TimeLeft = d
	tm.s.Broadcast(nmc.TimeLeft, d)
}

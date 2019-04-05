package server

import (
	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/protocol/gamemode"
)

func (s *Server) StartMode(id gamemode.ID) game.TimedMode {
	var gen func(game.Timed) game.TimedMode

	switch id {
	case gamemode.Insta:
		gen = func(t game.Timed) game.TimedMode { return game.NewInsta(s, t) }
	case gamemode.InstaTeam:
		gen = func(t game.Timed) game.TimedMode { return game.NewInstaTeam(s, s.KeepTeams, t) }
	case gamemode.Effic:
		gen = func(t game.Timed) game.TimedMode { return game.NewEffic(s, t) }
	case gamemode.EfficTeam:
		gen = func(t game.Timed) game.TimedMode { return game.NewEfficTeam(s, s.KeepTeams, t) }
	case gamemode.Tactics:
		gen = func(t game.Timed) game.TimedMode { return game.NewTactics(s, t) }
	case gamemode.TacticsTeam:
		gen = func(t game.Timed) game.TimedMode { return game.NewTacticsTeam(s, s.KeepTeams, t) }
	case gamemode.InstaCTF:
		gen = func(t game.Timed) game.TimedMode { return game.NewInstaCTF(s, s.KeepTeams, t) }
	case gamemode.EfficCTF:
		gen = func(t game.Timed) game.TimedMode { return game.NewEfficCTF(s, s.KeepTeams, t) }
	default:
		return nil
	}

	if s.CompetitiveMode {
		return game.NewCompetitiveMode(s, gen)
	}
	return gen(game.NewTimedMode(s))
}

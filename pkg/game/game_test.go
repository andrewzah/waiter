package game

import (
	"fmt"
	"testing"
	"time"

	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
)

var (
	_ Server = &mockServer{}
	//_ Player = &mockPlayer{}
)

type mockServer struct{}

func (s *mockServer) GameDuration() time.Duration { return 10 * time.Minute }

func (s *mockServer) Broadcast(nmc.ID, ...interface{}) {}

func (s *mockServer) Intermission() {}

func (s *mockServer) ForEach(func(*Player)) {}

func (s *mockServer) UniqueName(p *Player) string { return fmt.Sprintf("%v", p) }

func TestCompetitiveMode(t *testing.T) {
	srv := &mockServer{}

	mode := NewCompetitiveMode(srv, func(t Timed) TimedMode { return NewEfficCTF(srv, false, t) })

	teamMode, ok := mode.(TeamedMode)
	if !ok {
		t.Error("effic ctf is not a team mode")
		return
	}

	p1, p2 := NewPlayer(1), NewPlayer(2)

	teamMode.Join(&p1)

	if countPlayers(teamMode) != 1 {
		t.Error("after one player joined, player count is not 1")
	}

	teamMode.Join(&p2)

	if countPlayers(teamMode) != 2 {
		t.Error("after two players joined, player count is not 2")
	}
}

func countPlayers(tm Teamed) (sum int) {
	tm.ForEach(func(t *Team) { sum += len(t.Players) })
	return
}

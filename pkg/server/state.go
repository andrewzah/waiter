package server

import (
	"time"

	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/protocol/mastermode"
)

type State struct {
	Clock      game.Clock
	MasterMode mastermode.ID
	GameMode   game.Mode
	Map        string
	UpSince    time.Time
	NumClients func() int // number of clients connected
}

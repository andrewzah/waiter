package server

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/sauerbraten/waiter/pkg/enet"
	"github.com/sauerbraten/waiter/pkg/protocol/cubecode"
	"github.com/sauerbraten/waiter/pkg/protocol/disconnectreason"
	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
)

type Clients struct {
	byCN   []*Client
	byPeer map[*enet.Peer]*Client
}

func newClients() *Clients {
	return &Clients{
		byPeer: map[*enet.Peer]*Client{},
	}
}

// Links an ENet peer to a client object. If no unused client object can be found, a new one is created and added to the global set of clients.
func (clients *Clients) add(peer *enet.Peer) *Client {
	// re-use unused client object with low cn
	for _, c := range clients.byCN {
		if c.Peer == nil {
			c.Peer = peer
			clients.byPeer[peer] = c
			return c
		}
	}

	cn := uint32(len(clients.byCN))
	c := NewClient(cn, peer)
	clients.byCN = append(clients.byCN, c)
	clients.byPeer[peer] = c
	return c
}

func (clients *Clients) clientByCN(cn uint32) *Client {
	if int(cn) < 0 || int(cn) >= len(clients.byCN) {
		return nil
	}
	return clients.byCN[cn]
}

func (clients *Clients) clientByPeer(peer *enet.Peer) *Client { return clients.byPeer[peer] }

func (clients *Clients) findClientByName(name string) *Client {
	name = strings.ToLower(name)
	for _, c := range clients.byPeer {
		if strings.Contains(c.Name, name) {
			return c
		}
	}
	return nil
}

// Send a packet to a client's team, but not the client himself, over the specified channel.
func (clients *Clients) SendToTeam(c *Client, typ nmc.ID, args ...interface{}) {
	excludeSelfAndOtherTeams := func(_c *Client) bool {
		return _c == c || _c.Team != c.Team
	}
	clients.broadcast(excludeSelfAndOtherTeams, typ, args...)
}

// Sends a packet to all clients currently in use.
func (clients *Clients) Broadcast(typ nmc.ID, args ...interface{}) {
	clients.broadcast(nil, typ, args...)
}

func (clients *Clients) broadcast(exclude func(*Client) bool, typ nmc.ID, args ...interface{}) {
	for _, c := range clients.byPeer {
		if exclude != nil && exclude(c) {
			continue
		}
		c.Send(typ, args...)
	}
}

func exclude(c *Client) func(*Client) bool {
	return func(_c *Client) bool {
		return _c == c
	}
}

func (clients *Clients) Relay(from *Client, typ nmc.ID, args ...interface{}) {
	clients.broadcast(exclude(from), typ, args...)
}

// Tells other clients that the client disconnected, giving a disconnect reason in case it's not a normal leave.
func (clients *Clients) Disconnect(c *Client, reason disconnectreason.ID) *enet.Peer {
	peer := c.Peer
	if peer == nil {
		return nil
	}

	clients.Relay(c, nmc.Leave, c.CN)

	msg := ""
	if reason != disconnectreason.None {
		msg = fmt.Sprintf("%s (%s) disconnected because: %s", clients.UniqueName(c), peer.Address.IP, reason)
		clients.Relay(c, nmc.ServerMessage, msg)
	} else {
		msg = fmt.Sprintf("%s (%s) disconnected", clients.UniqueName(c), peer.Address.IP)
	}
	log.Println(cubecode.SanitizeString(msg))

	c.Reset()
	delete(clients.byPeer, peer)

	return peer
}

// Returns the number of connected clients.
func (clients *Clients) NumberOfClientsConnected() int { return len(clients.byPeer) }

func (clients *Clients) ForEach(do func(c *Client)) {
	for _, c := range clients.byPeer {
		do(c)
	}
}

func (clients *Clients) UniqueName(c *Client) string {
	unique := true
	clients.ForEach(func(_c *Client) {
		if _c != c && _c.Name == c.Name {
			unique = false
		}
	})

	if unique {
		return c.Name
	}
	return c.Name + cubecode.Magenta(" ("+strconv.FormatUint(uint64(c.CN), 10)+")")
}

func (clients *Clients) PrivilegedUsers() (privileged []*Client) {
	clients.ForEach(func(c *Client) {
		if c.Role > role.None {
			privileged = append(privileged, c)
		}
	})
	return
}

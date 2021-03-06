package main

import (
	"log"
	"net"
	"strconv"
	"time"

	"github.com/sauerbraten/waiter/internal/net/packet"
	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/protocol"
	"github.com/sauerbraten/waiter/pkg/server"
)

// Protocol constants
const (
	// Constants describing the type of information to query for
	InfoTypeExtended int32 = 0

	// Constants used in responses to extended info queries
	ExtInfoACK     int32 = -1  // EXT_ACK
	ExtInfoVersion int32 = 105 // EXT_VERSION
	ExtInfoNoError int32 = 0   // EXT_NO_ERROR
	ExtInfoError   int32 = 1   // EXT_ERROR

	// Constants describing the type of extended information to query for
	ExtInfoTypeUptime     int32 = 0 // EXT_UPTIME
	ExtInfoTypeClientInfo int32 = 1 // EXT_PLAYERSTATS
	ExtInfoTypeTeamScores int32 = 2 // EXT_TEAMSCORE

	// Constants used in responses to client info queries
	ClientInfoResponseTypeCNs  int32 = -10 // EXT_PLAYERSTATS_RESP_IDS
	ClientInfoResponseTypeInfo int32 = -11 // EXT_PLAYERSTATS_RESP_STATS

	// ID to identify this server mod via extinfo
	ServerMod int32 = -9
)

type infoRequest struct {
	raddr   *net.UDPAddr
	payload []byte
}

type infoServer struct {
	s    *server.Server
	conn *net.UDPConn
}

func StartListeningForInfoRequests(s *server.Server) (*infoServer, <-chan infoRequest) {
	laddr, err := net.ResolveUDPAddr("udp", s.ListenAddress+":"+strconv.Itoa(s.ListenPort+1))
	if err != nil {
		log.Println(err)
		return nil, nil
	}

	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Println(err)
		return nil, nil
	}

	inc := make(chan infoRequest)

	go func() {
		for {
			pkt := make(protocol.Packet, 128)
			n, raddr, err := conn.ReadFromUDP(pkt)
			if err != nil {
				log.Println(err)
				continue
			}
			inc <- infoRequest{
				raddr:   raddr,
				payload: pkt[:n],
			}
		}
	}()

	log.Println("listening for info requests on", laddr.String())

	return &infoServer{
		s:    s,
		conn: conn,
	}, inc
}

func (i *infoServer) Handle(req infoRequest) {
	// prepare response header (we need to replay the request)
	respHeader := req.payload

	// interpret request as packet
	p := protocol.Packet(req.payload)

	reqType, ok := p.GetInt()
	if !ok {
		log.Println("extinfo: info request packet too short: could not read request type:", p)
		return
	}

	switch reqType {
	case InfoTypeExtended:
		extReqType, ok := p.GetInt()
		if !ok {
			log.Println("malformed info request: could not read extinfo request type:", p)
			return
		}
		switch extReqType {
		case ExtInfoTypeUptime:
			i.send(req.raddr, i.uptime(respHeader))
		case ExtInfoTypeClientInfo:
			cn, ok := p.GetInt()
			if !ok {
				log.Println("malformed info request: could not read CN from client info request:", p)
				return
			}
			i.send(req.raddr, i.clientInfo(cn, respHeader)...)
		case ExtInfoTypeTeamScores:
			i.send(req.raddr, i.teamScores(respHeader))
		default:
			log.Println("erroneous extinfo type queried:", reqType)
		}
	default:
		i.send(req.raddr, i.basicInfo(respHeader))
	}
}

func (i *infoServer) send(raddr *net.UDPAddr, packets ...protocol.Packet) {
	for _, p := range packets {
		n, err := i.conn.WriteToUDP(p, raddr)
		if err != nil {
			log.Println(err)
		}

		if n != len(p) {
			log.Println("packet length and sent length didn't match!", p)
		}
	}
}

func (i *infoServer) basicInfo(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		s.NumClients(),
	}

	timeLeft := int32(s.Clock.TimeLeft() / time.Second)
	paused := s.Clock.Paused()

	if paused {
		q = append(q, 7)
	} else {
		q = append(q, 5)
	}

	q = append(q,
		protocol.Version,
		s.GameMode.ID(),
		timeLeft,
		s.MaxClients,
		s.MasterMode,
	)

	if paused {
		q = append(q,
			paused, // paused?
			100,    // gamespeed
		)
	}

	q = append(q, s.Map, s.ServerDescription)

	return packet.Encode(q...)
}

func (i *infoServer) uptime(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
		int32(time.Since(s.UpSince) / time.Second),
	}

	if len(respHeader) > 2 {
		q = append(q, ServerMod)
	}

	return packet.Encode(q...)
}

func (i *infoServer) clientInfo(cn int32, respHeader []byte) (packets []protocol.Packet) {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
	}

	if cn < -1 || int(cn) > s.NumClients() {
		q = append(q, ExtInfoError)
		packets = append(packets, packet.Encode(q...))
		return
	}

	q = append(q, ExtInfoNoError)

	header := q

	q = append(q, ClientInfoResponseTypeCNs)

	if cn == -1 {
		s.Clients.ForEach(func(c *server.Client) { q = append(q, c.CN) })
	} else {
		q = append(q, cn)
	}

	packets = append(packets, packet.Encode(q...))

	if cn == -1 {
		s.Clients.ForEach(func(c *server.Client) {
			packets = append(packets, i.clientPacket(c, header))
		})
	} else {
		c := s.Clients.GetClientByCN(uint32(cn))
		packets = append(packets, i.clientPacket(c, header))
	}

	return
}

func max(i, j int32) int32 {
	if i > j {
		return i
	}
	return j
}

func (i *infoServer) clientPacket(c *server.Client, header []interface{}) protocol.Packet {
	q := header

	q = append(q,
		ClientInfoResponseTypeInfo,
		c.CN,
		c.Ping,
		c.Name,
		c.Team.Name,
		c.Frags,
		c.Flags,
		c.Deaths,
		c.Teamkills,
		c.Damage*100/max(c.DamagePotential, 1),
		c.Health,
		c.Armour,
		c.SelectedWeapon.ID,
		c.Role,
		c.State,
	)

	if s.SendClientIPsViaExtinfo {
		q = append(q, []byte(c.Peer.Address.IP.To4()[:3]))
	} else {
		q = append(q, 0, 0, 0)
	}

	return packet.Encode(q...)
}

func (i *infoServer) teamScores(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
	}

	teamMode, isTeamMode := s.GameMode.(game.TeamMode)
	if isTeamMode {
		q = append(q, ExtInfoNoError)
	} else {
		q = append(q, ExtInfoError)
	}

	q = append(q, s.GameMode.ID())

	q = append(q, int32(s.Clock.TimeLeft()/time.Second))

	if !isTeamMode {
		return packet.Encode(q...)
	}

	captureMode, isCaptureMode := teamMode.(game.CaptureMode)

	for name, team := range teamMode.Teams() {
		if team.Score <= 0 && len(team.Players) == 0 {
			continue
		}
		q = append(q, name, team.Score)
		if isCaptureMode {
			bases := captureMode.Bases(team)
			q = append(q, len(bases), bases)
		} else {
			q = append(q, -1)
		}
	}

	return packet.Encode(q...)
}

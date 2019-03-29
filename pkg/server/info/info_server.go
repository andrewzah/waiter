package info

import (
	"log"
	"net"
	"time"

	"github.com/sauerbraten/waiter/pkg/protocol/role"

	"github.com/sauerbraten/waiter/pkg/protocol/mastermode"

	"github.com/sauerbraten/waiter/internal/net/packet"
	"github.com/sauerbraten/waiter/pkg/game"
	"github.com/sauerbraten/waiter/pkg/protocol"
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

type ServerState interface {
	MasterMode() mastermode.ID
	GameMode() game.Mode
	Map() string
}

type Server interface {
	ServerState
	NumPlayers() int
	MaxPlayers() int
	Player(cn uint32) game.Player
	AllPlayers() []game.Player
	Ping(cn uint32) int32
	Role(cn uint32) role.ID
	IP(cn uint32) net.IP
}

type Request struct {
	raddr   *net.UDPAddr
	payload []byte
}

type InfoServer struct {
	serverDescription       string
	upSince                 time.Time
	server                  Server
	sendClientIPsViaExtinfo bool
	conn                    *net.UDPConn
}

func NewInfoServer(addr, description string, sendClientIPsViaExtinfo bool, server Server) (*InfoServer, <-chan Request) {
	laddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		log.Println(err)
		return nil, nil
	}

	conn, err := net.ListenUDP("udp", laddr)
	if err != nil {
		log.Println(err)
		return nil, nil
	}

	inc := make(chan Request)

	go func() {
		for {
			pkt := make(protocol.Packet, 128)
			n, raddr, err := conn.ReadFromUDP(pkt)
			if err != nil {
				log.Println(err)
				continue
			}
			inc <- Request{
				raddr:   raddr,
				payload: pkt[:n],
			}
		}
	}()

	log.Println("listening for info requests on", laddr.String())

	return &InfoServer{
		serverDescription:       description,
		upSince:                 time.Now(),
		server:                  server,
		sendClientIPsViaExtinfo: sendClientIPsViaExtinfo,
		conn:                    conn,
	}, inc
}

func (i *InfoServer) Handle(req Request) {
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

func (i *InfoServer) send(raddr *net.UDPAddr, packets ...protocol.Packet) {
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

func (i *InfoServer) basicInfo(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		i.server.NumPlayers(),
	}

	timedMode, isTimedMode := i.server.GameMode().(game.TimedMode)
	timeLeft, paused := 0*time.Second, false
	if isTimedMode {
		timeLeft = timedMode.TimeLeft()
		paused = timedMode.Paused()
	}

	if paused {
		q = append(q, 7)
	} else {
		q = append(q, 5)
	}

	q = append(q,
		protocol.Version,
		i.server.GameMode().ID(),
		timeLeft,
		i.server.MaxPlayers(),
		i.server.MasterMode(),
	)

	if paused {
		q = append(q,
			paused,
			100, // gamespeed
		)
	}

	q = append(q, i.server.Map(), i.serverDescription)

	return packet.Encode(q...)
}

func (i *InfoServer) uptime(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
		int32(time.Since(i.upSince) / time.Second),
	}

	if len(respHeader) > 2 {
		q = append(q, ServerMod)
	}

	return packet.Encode(q...)
}

func (i *InfoServer) clientInfo(cn int32, respHeader []byte) (packets []protocol.Packet) {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
	}

	if cn < -1 || int(cn) > i.server.NumPlayers() {
		q = append(q, ExtInfoError)
		packets = append(packets, packet.Encode(q...))
		return
	}

	q = append(q, ExtInfoNoError)

	header := q

	q = append(q, ClientInfoResponseTypeCNs)

	if cn == -1 {
		for _, player := range i.server.AllPlayers() {
			q = append(q, player.CN)
		}
	} else {
		q = append(q, cn)
	}

	packets = append(packets, packet.Encode(q...))

	if cn == -1 {
		for _, player := range i.server.AllPlayers() {
			packets = append(packets, i.clientPacket(player, header))
		}
	} else {
		player := i.server.Player(uint32(cn))
		packets = append(packets, i.clientPacket(player, header))
	}

	return
}

func max(i, j int32) int32 {
	if i > j {
		return i
	}
	return j
}

func (i *InfoServer) clientPacket(p game.Player, header []interface{}) protocol.Packet {
	q := header

	q = append(q,
		ClientInfoResponseTypeInfo,
		p.CN,
		i.server.Ping(p.CN),
		p.Name,
		p.Team.Name,
		p.Frags,
		p.Flags,
		p.Deaths,
		p.Teamkills,
		p.Damage*100/max(p.DamagePotential, 1),
		p.Health,
		p.Armour,
		p.SelectedWeapon.ID,
		i.server.Role(p.CN),
		p.State,
	)

	if i.sendClientIPsViaExtinfo {
		q = append(q, []byte(i.server.IP(p.CN).To4()[:3]))
	} else {
		q = append(q, 0, 0, 0)
	}

	return packet.Encode(q...)
}

func (i *InfoServer) teamScores(respHeader []byte) protocol.Packet {
	q := []interface{}{
		respHeader,
		ExtInfoACK,
		ExtInfoVersion,
	}

	teamMode, isTeamMode := i.server.GameMode().(game.TeamMode)
	if isTeamMode {
		q = append(q, ExtInfoNoError)
	} else {
		q = append(q, ExtInfoError)
	}

	timedMode, isTimedMode := i.server.GameMode().(game.TimedMode)
	timeLeft := 0 * time.Second
	if isTimedMode {
		timeLeft = timedMode.TimeLeft()
	}

	q = append(q, i.server.GameMode().ID(), timeLeft)

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

package packet

import (
	"log"
	"net"

	"github.com/sauerbraten/waiter/pkg/geom"
	"github.com/sauerbraten/waiter/pkg/protocol"
	"github.com/sauerbraten/waiter/pkg/protocol/armour"
	"github.com/sauerbraten/waiter/pkg/protocol/disconnectreason"
	"github.com/sauerbraten/waiter/pkg/protocol/entity"
	"github.com/sauerbraten/waiter/pkg/protocol/gamemode"
	"github.com/sauerbraten/waiter/pkg/protocol/mastermode"
	"github.com/sauerbraten/waiter/pkg/protocol/nmc"
	"github.com/sauerbraten/waiter/pkg/protocol/playerstate"
	"github.com/sauerbraten/waiter/pkg/protocol/role"
	"github.com/sauerbraten/waiter/pkg/protocol/sound"
	"github.com/sauerbraten/waiter/pkg/protocol/weapon"
)

func Encode(args ...interface{}) protocol.Packet {
	if len(args) == 0 {
		return nil
	}

	p := make(protocol.Packet, 0, len(args))

	for _, arg := range args {
		switch v := arg.(type) {
		case int32:
			p.PutInt(v)

		case []int32:
			for _, w := range v {
				p.PutInt(w)
			}

		case int:
			p.PutInt(int32(v))

		case uint32:
			// you'll have to be explicit and call p.PutUint() if you
			// really want that!
			p.PutInt(int32(v))

		case float64:
			p.PutInt(int32(v))

		case byte:
			p = append(p, v)

		case playerstate.ID:
			p.PutInt(int32(v))

		case armour.ID:
			p.PutInt(int32(v))

		case entity.ID:
			p.PutInt(int32(v))

		case gamemode.ID:
			p.PutInt(int32(v))

		case mastermode.ID:
			p.PutInt(int32(v))

		case nmc.ID:
			p.PutInt(int32(v))

		case role.ID:
			p.PutInt(int32(v))

		case sound.ID:
			p.PutInt(int32(v))

		case weapon.ID:
			p.PutInt(int32(v))

		case disconnectreason.ID:
			p.PutInt(int32(v))

		case []byte:
			p = append(p, v...)

		case protocol.Packet:
			p = append(p, v...)

		case net.IP:
			p = append(p, v...)

		case bool:
			if v {
				p = append(p, 1)
			} else {
				p = append(p, 0)
			}

		case string:
			p.PutString(v)

		case *geom.Vector:
			p.PutInt(int32(v.X()))
			p.PutInt(int32(v.Y()))
			p.PutInt(int32(v.Z()))

		default:
			log.Printf("unhandled type %T of arg %v\n", v, v)
		}
	}

	return p
}

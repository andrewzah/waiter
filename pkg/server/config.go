package server

import (
	"strings"
	"time"

	"github.com/sauerbraten/waiter/pkg/maprot"
	"github.com/sauerbraten/waiter/pkg/protocol/gamemode"
)

type duration time.Duration

func (d *duration) UnmarshalJSON(b []byte) error {
	_d, err := time.ParseDuration(strings.Trim(string(b), `"`))
	*d = duration(_d)
	return err
}

type Config struct {
	ListenAddress string `json:"listen_address"`
	ListenPort    int    `json:"listen_port"`

	MasterServerAddress   string `json:"master_server_address"`
	StatsServerAddress    string `json:"stats_server_address"`
	StatsServerAuthDomain string `json:"stats_server_auth_domain"`

	ServerDescription       string       `json:"server_description"`
	MessageOfTheDay         string       `json:"message_of_the_day"`
	MaxClients              int          `json:"max_clients"`
	FallbackGameMode        gamemode.ID  `json:"fallback_game_mode"`
	GameDuration            duration     `json:"game_duration"`
	AuthDomain              string       `json:"auth_domain"`
	SendClientIPsViaExtinfo bool         `json:"send_client_ips_via_extinfo"`
	MapPools                maprot.Pools `json:"maps"`
}

var DefaultConfig = Config{
	ListenPort: 28785,

	MasterServerAddress:   "master.sauerbraten.org:28787",
	StatsServerAddress:    "stats.p1x.pw:28787",
	StatsServerAuthDomain: "stats.p1x.pw",

	ServerDescription:       "waiter",
	MessageOfTheDay:         "to play other maps, add them to the map pools in the server configuration",
	MaxClients:              12,
	FallbackGameMode:        gamemode.Effic,
	GameDuration:            duration(10 * time.Minute),
	AuthDomain:              "local",
	SendClientIPsViaExtinfo: true,
	MapPools: maprot.Pools{
		Deathmatch: []string{"ot", "turbine", "memento"},
		CTF:        []string{"reissen", "forge", "dust2"},
		Capture:    []string{"nmp4", "nmp8", "nmp9"},
	},
}

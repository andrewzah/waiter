package main

import (
	"time"

	"github.com/sauerbraten/waiter/internal/definitions/armour"
	"github.com/sauerbraten/waiter/internal/definitions/gamemode"
	"github.com/sauerbraten/waiter/internal/definitions/playerstate"
	"github.com/sauerbraten/waiter/internal/definitions/weapon"
	"github.com/sauerbraten/waiter/internal/net/packet"
)

// The game state of a client.
type GameState struct {
	// fields that change at spawn
	State          uint32
	Health         int32
	MaxHealth      int32
	Armour         int32
	ArmourType     armour.ID
	QuadTimeLeft   int32 // in milliseconds
	SelectedWeapon weapon.Weapon
	GunReloadEnd   time.Time
	Ammo           map[weapon.ID]int32 // weapon → ammo
	Tokens         int32               // skulls

	LastSpawnAttempt time.Time
	LifeSequence     int32
	LastShot         time.Time
	LastDeath        time.Time

	// fields that change at intermission
	Frags      int
	Deaths     int
	Teamkills  int
	ShotDamage int32
	Damage     int32
	Flags      int
}

func NewGameState() *GameState {
	gs := &GameState{}
	gs.Reset()
	return gs
}

func (gs *GameState) ToWire() []byte {
	return packet.Encode(
		gs.LifeSequence,
		gs.Health,
		gs.MaxHealth,
		gs.Armour,
		gs.ArmourType,
		gs.SelectedWeapon.ID,
		weapon.FlattenAmmo(gs.Ammo),
	)
}

// Sets GameState properties to the initial values depending on the mode.
func (gs *GameState) Spawn(mode gamemode.ID) {
	gs.QuadTimeLeft = 0
	gs.GunReloadEnd = time.Time{}
	gs.Tokens = 0
	gs.ArmourType, gs.Armour = armour.SpawnArmour(mode)
	gs.Ammo, gs.SelectedWeapon = weapon.SpawnAmmo(mode)
	gs.LastSpawnAttempt = time.Now()
	gs.LifeSequence = (gs.LifeSequence + 1) % 128

	gs.Health = gs.MaxHealth

	switch mode {
	case gamemode.Insta,
		gamemode.InstaTeam,
		gamemode.InstaCTF,
		gamemode.InstaProtect,
		gamemode.InstaHold,
		gamemode.InstaCollect:
		gs.Health, gs.MaxHealth = 1, 1
	default:
		// maxhealth/100 is fine
	}
}

func (gs *GameState) SelectWeapon(id weapon.ID) (weapon.Weapon, bool) {
	if gs.State != playerstate.Alive {
		return weapon.ByID(weapon.Pistol), false
	}
	gs.SelectedWeapon = weapon.ByID(id)
	return gs.SelectedWeapon, true
}

func (gs *GameState) applyDamage(damage int32) {
	// TODO: account for armour
	damageToArmour := damage * armour.Absorption(gs.ArmourType) / 100
	if damageToArmour > gs.Armour {
		damageToArmour = gs.Armour
	}
	gs.Armour -= damageToArmour
	damage -= damageToArmour
	gs.Health -= damage
}

func (gs *GameState) Die() {
	if gs.State != playerstate.Alive {
		return
	}
	gs.State = playerstate.Dead
	gs.Deaths++
	gs.LastDeath = time.Now()
	gs.LastShot = time.Time{}
}

// Resets a client's game state.
func (gs *GameState) Reset() {
	if gs.State != playerstate.Spectator {
		gs.State = playerstate.Dead
	}
	gs.MaxHealth = 100

	gs.LifeSequence = 0
	gs.LastDeath = time.Time{}

	gs.Frags = 0
	gs.Deaths = 0
	gs.Teamkills = 0
	gs.ShotDamage = 0
	gs.Damage = 0
	gs.Flags = 0
}

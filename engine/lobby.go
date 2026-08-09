package zztgo

const LobbyWorldName = "LOBBY"
const ArenaWorldName = "ARENA"

var defaultLobbyTransitGates = map[TransitGateKey]string{
	{BoardID: 1, X: 12, Y: 9}: "WELCOME",
	{BoardID: 1, X: 30, Y: 9}: "TOWN",
	{BoardID: 1, X: 48, Y: 9}: ArenaWorldName,
}

var defaultArenaTransitGates = map[TransitGateKey]string{
	{BoardID: 1, X: 30, Y: 22}: LobbyWorldName,
}

func configureLobbyTransits(inst *WorldInstance) {
	if inst == nil || inst.RoomManager == nil {
		return
	}
	var gates map[TransitGateKey]string
	switch inst.Name {
	case LobbyWorldName:
		gates = defaultLobbyTransitGates
	case ArenaWorldName:
		gates = defaultArenaTransitGates
	default:
		return
	}
	inst.RoomManager.TransitGates = make(map[TransitGateKey]string, len(gates))
	for gate, destination := range gates {
		inst.RoomManager.TransitGates[gate] = destination
	}
}

func friendlyFireForWorldIdentity(name string) bool {
	safe, err := SanitizeSaveName(name)
	return err == nil && safe == ArenaWorldName
}

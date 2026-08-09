package zztgo

const LobbyWorldName = "LOBBY"

var defaultLobbyTransitGates = map[TransitGateKey]string{
	{BoardID: 1, X: 12, Y: 9}: "WELCOME",
	{BoardID: 1, X: 30, Y: 9}: "TOWN",
}

func configureLobbyTransits(inst *WorldInstance) {
	if inst == nil || inst.Name != LobbyWorldName || inst.RoomManager == nil {
		return
	}
	inst.RoomManager.TransitGates = make(map[TransitGateKey]string, len(defaultLobbyTransitGates))
	for gate, destination := range defaultLobbyTransitGates {
		inst.RoomManager.TransitGates[gate] = destination
	}
}

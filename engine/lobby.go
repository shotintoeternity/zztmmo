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

// GazetteNoticeKind is the notice the lobby's newsstand posts (M34.3).
const GazetteNoticeKind = "gazette"

// defaultLobbyNotices names the tile the Gazette stand occupies in Central
// Hall. It sits in the lower left, deliberately off every square the lobby and
// arena browser journeys walk — column 30 and column 48 between y=9 and y=13,
// and row 13 between them — because a scroll on a walked square eats the arrows
// the rest of a journey needs (M16.11d). fixtures/lobby.zwd is the other half
// of this pair, and a test asserts they still agree.
var defaultLobbyNotices = map[TransitGateKey]string{
	{BoardID: 1, X: 13, Y: 20}: GazetteNoticeKind,
}

func configureLobbyTransits(inst *WorldInstance) {
	if inst == nil || inst.RoomManager == nil {
		return
	}
	var gates map[TransitGateKey]string
	var notices map[TransitGateKey]string
	switch inst.Name {
	case LobbyWorldName:
		gates = defaultLobbyTransitGates
		notices = defaultLobbyNotices
	case ArenaWorldName:
		gates = defaultArenaTransitGates
	default:
		return
	}
	inst.RoomManager.TransitGates = make(map[TransitGateKey]string, len(gates))
	for gate, destination := range gates {
		inst.RoomManager.TransitGates[gate] = destination
	}
	if len(notices) == 0 {
		return
	}
	inst.RoomManager.NoticeTiles = make(map[TransitGateKey]string, len(notices))
	for tile, kind := range notices {
		inst.RoomManager.NoticeTiles[tile] = kind
	}
}

func friendlyFireForWorldIdentity(name string) bool {
	safe, err := SanitizeSaveName(name)
	return err == nil && safe == ArenaWorldName
}

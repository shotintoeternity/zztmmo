package zztgo

import "testing"

func TestM74PerPlayerSoundAttribution(t *testing.T) {
	setup := NewEngine()
	setup.Headless = true
	setup.WorldCreate()
	setup.BoardCreate()
	setup.BoardClose()

	rm := NewRoomManager(setup.World)
	playerA := rm.JoinPlayer(0, 10, 10)
	playerB := rm.JoinPlayer(0, 20, 10)
	room, ok := rm.Room(0)
	if !ok {
		t.Fatal("room 0 missing")
	}
	_, statA, ok := rm.PlayerLocation(playerA)
	if !ok {
		t.Fatal("player A missing")
	}
	a := room.Engine.Board.Stats[statA]
	room.Engine.Board.Tiles[int16(a.X)+1][a.Y] = TTile{Element: E_GEM, Color: 0x0B}

	diffs := rm.StepDiffs(map[PlayerID]PlayerInput{
		playerA: {DeltaX: 1},
	})
	aEvents := rm.DrainPlayerEvents(playerA)
	bEvents := rm.DrainPlayerEvents(playerB)

	if !eventsHaveSound(aEvents, 2, "@\x017\x014\x010\x01") {
		t.Fatalf("player A did not receive gem sound: %#v", aEvents)
	}
	if eventsHaveAnySound(bEvents) {
		t.Fatalf("player B received player A's gem sound: %#v", bEvents)
	}
	if protocolEventsHaveAnySound(diffs[playerA].Events) || protocolEventsHaveAnySound(diffs[playerB].Events) {
		t.Fatalf("gem sound leaked through room diffs: A=%#v B=%#v", diffs[playerA].Events, diffs[playerB].Events)
	}

	room.Engine.AddStat(5, 5, E_OBJECT, 0x0F, 1, StatTemplateDefault)
	object := &room.Engine.Board.Stats[room.Engine.Board.StatCount]
	object.Data = "@obj\r#play c\r#end\r"
	object.DataLen = int16(len(object.Data))
	object.DataPos = 0

	diffs = rm.StepDiffs(map[PlayerID]PlayerInput{})
	if !protocolEventsHaveAnySound(diffs[playerA].Events) {
		t.Fatalf("player A did not receive room-wide object #play sound: %#v", diffs[playerA].Events)
	}
	if !protocolEventsHaveAnySound(diffs[playerB].Events) {
		t.Fatalf("player B did not receive room-wide object #play sound: %#v", diffs[playerB].Events)
	}
}

func eventsHaveAnySound(events []Event) bool {
	for _, event := range events {
		if _, ok := event.(SoundEvent); ok {
			return true
		}
	}
	return false
}

func eventsHaveSound(events []Event, priority int16, notes string) bool {
	for _, event := range events {
		sound, ok := event.(SoundEvent)
		if ok && sound.Priority == priority && sound.Notes == notes {
			return true
		}
	}
	return false
}

func protocolEventsHaveAnySound(events []ProtocolEvent) bool {
	for _, event := range events {
		if event.Type == "sound" {
			return true
		}
	}
	return false
}

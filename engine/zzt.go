// ZZT ported to Go

/*
TODO:
- ensure len>0 for things like: switch state.Lines[lpos-1][0]
- timer handling, timeout of message display
- editor: EditorTransferBoard
- sounds
- performance
  + optimize when to call VideoShow
  + stat.Data should probably be []byte instead of string
*/

package main

func main() {
	E.WorldFileDescCount = 7
	E.WorldFileDescKeys[0] = "TOWN"
	E.WorldFileDescValues[0] = "TOWN       The Town of ZZT"
	E.WorldFileDescKeys[1] = "DEMO"
	E.WorldFileDescValues[1] = "DEMO       Demo of the ZZT World Editor"
	E.WorldFileDescKeys[2] = "CAVES"
	E.WorldFileDescValues[2] = "CAVES      The Caves of ZZT"
	E.WorldFileDescKeys[3] = "DUNGEONS"
	E.WorldFileDescValues[3] = "DUNGEONS   The Dungeons of ZZT"
	E.WorldFileDescKeys[4] = "CITY"
	E.WorldFileDescValues[4] = "CITY       Underground City of ZZT"
	E.WorldFileDescKeys[5] = "BEST"
	E.WorldFileDescValues[5] = "BEST       The Best of ZZT"
	E.WorldFileDescKeys[6] = "TOUR"
	E.WorldFileDescValues[6] = "TOUR       Guided Tour ZZT's Other Worlds"

	E.StartupWorldFileName = "TOWN"
	ResourceDataFileName = "ZZT.DAT"
	E.GameTitleExitRequested = false
	E.EditorEnabled = true

	VideoInstall()
	TextWindowInit(5, 3, 50, 18)
	VideoHideCursor()
	VideoClrScr()
	E.TickSpeed = 4
	E.DebugEnabled = false
	E.SavedGameFileName = "SAVED"
	E.SavedBoardFileName = "TEMP"
	GenerateTransitionTable()
	WorldCreate()

	GameTitleLoop()

	SoundUninstall()
	SoundClearQueue()
	VideoUninstall()
}

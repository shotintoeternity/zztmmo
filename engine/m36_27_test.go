package zztgo

import (
	"strings"
	"testing"
)

// M36.27e: a dialogue line that already fits is the author's exact bytes.
// Reflowing it through strings.Fields collapsed its spaces, so ASCII art in a
// scroll compiled flat, silently.
func TestM3627ZWDKeepsLinesThatAlreadyFit(t *testing.T) {
	art := []string{
		"   /\\_/\\",
		"  ( o.o )   meow",
		"$  centered   with  gaps  ",
		"!pet;  Pet  the   cat",
	}
	got, err := wrapZWDTextWindowLines(strings.Join(art, "\r"))
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Join(art, "\r") {
		t.Fatalf("lines within the limit were rewritten:\n got %q\nwant %q", strings.Split(got, "\r"), art)
	}

	// An overlong line is still wrapped to the window.
	long := strings.Repeat("word ", 20)
	got, err = wrapZWDTextWindowLines(long)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(got, "\r") {
		if len(line) > zztTextWindowLineWidth {
			t.Fatalf("an overlong line was not wrapped: %q", line)
		}
	}
}

// m3627WorldWithProgram compiles the one-room example and replaces its first
// object's program with data, returning the world.
func m3627WorldWithProgram(t *testing.T, data string) TWorld {
	t.Helper()
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	for b := int16(0); b <= e.World.BoardCount; b++ {
		e.BoardOpen(b)
		for i := int16(1); i <= e.Board.StatCount; i++ {
			if e.Board.Stats[i].DataLen > 0 {
				e.Board.Stats[i].Data = data
				e.Board.Stats[i].DataLen = int16(len(data))
				e.BoardClose()
				return e.World
			}
		}
	}
	t.Fatal("the example world has no object with a program")
	return TWorld{}
}

// m3627FirstProgram compiles src and returns the first stat program it holds.
func m3627FirstProgram(t *testing.T, src string) string {
	t.Helper()
	world, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatalf("recompile: %v\n%s", err, src)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	for b := int16(0); b <= e.World.BoardCount; b++ {
		e.BoardOpen(b)
		for i := int16(1); i <= e.Board.StatCount; i++ {
			if e.Board.Stats[i].DataLen > 0 {
				return e.Board.Stats[i].Data
			}
		}
	}
	t.Fatal("no program after recompile")
	return ""
}

// M36.27e: decompile -> compile keeps an object's program byte for byte when
// its lines are indented. The decompiler wrote them at column 0 under an
// indented `oop`, and the parser strips that indent, so four leading spaces
// were lost on the way back in, silently.
func TestM3627DecompiledProgramKeepsIndentation(t *testing.T) {
	program := strings.Join([]string{"@cat", "    Indented  dialogue", "  two", "#end", ""}, "\r")
	world := m3627WorldWithProgram(t, program)
	src, diagnostics := DecompileZWDAuthorable(&world)
	if src == "" {
		t.Fatalf("decompile refused the world: %#v", diagnostics)
	}
	if got := m3627FirstProgram(t, src); got != program {
		t.Fatalf("program after decompile/compile:\n got %q\nwant %q", got, program)
	}
}

// M36.27e: a program line reading "end" closes a ZWD oop block wherever it is
// indented, so it cannot be written. The decompiler now refuses it by name;
// it used to emit the source and fail later on an unrelated-looking parse.
func TestM3627DecompileRefusesAProgramLineReadingEnd(t *testing.T) {
	program := strings.Join([]string{"@cat", "  end", "#end", ""}, "\r")
	world := m3627WorldWithProgram(t, program)
	src, diagnostics := DecompileZWDAuthorable(&world)
	if src != "" {
		t.Fatal("decompile emitted source for a program it cannot represent")
	}
	named := false
	for _, d := range diagnostics {
		if d.Severity == "error" && strings.Contains(d.Message, "program line 2") {
			named = true
		}
	}
	if !named {
		t.Fatalf("diagnostics do not name the offending line: %#v", diagnostics)
	}
}

// M36.27e: the compiler refuses what it would otherwise drop. A board message
// over the 58 bytes the board info stores was cut silently by StoreString, and
// a legend `to "BOARD"` naming no board compiled as if it were fine.
func TestM3627ZWDRefusesWhatItWouldTruncateOrIgnore(t *testing.T) {
	withMessage := func(msg string) string {
		return strings.Replace(zwdOneRoomExample, "  time-limit 0\n", "  time-limit 0\n  message \""+msg+"\"\n", 1)
	}
	if _, err := CompileZWD(withMessage(strings.Repeat("m", SizeOfBoardInfoMessage-1))); err != nil {
		t.Fatalf("a %d-byte message must compile: %v", SizeOfBoardInfoMessage-1, err)
	}
	if _, err := CompileZWD(withMessage(strings.Repeat("m", SizeOfBoardInfoMessage))); err == nil ||
		!strings.Contains(err.Error(), "message") {
		t.Errorf("a %d-byte message compiled (err %v); it would be cut to %d", SizeOfBoardInfoMessage, err, SizeOfBoardInfoMessage-1)
	}

	unresolved := strings.Replace(zwdOneRoomExample, "legend\n", "legend\n  p = Passage color 0x2F to \"Nowhere\"\n", 1)
	if unresolved == zwdOneRoomExample {
		t.Fatal("could not add a legend entry to the example")
	}
	if _, err := CompileZWD(unresolved); err == nil || !strings.Contains(err.Error(), "Nowhere") {
		t.Errorf("a legend `to` naming no board compiled (err %v)", err)
	}
}

// M36.27e: decompile keeps a turning conveyor's stat. Conveyors may carry a
// stat (elementMayHaveStat) and turn only when they do, but the decompiler
// asked elementNeedsStat and dropped it, so the recompiled conveyor froze.
func TestM3627DecompileKeepsConveyorStats(t *testing.T) {
	world, err := CompileZWDWorld(zwdOneRoomExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	e.BoardOpen(0)
	e.Board.Tiles[5][5] = TTile{Element: E_CONVEYOR_CW, Color: 0x0F}
	e.AddStat(5, 5, E_CONVEYOR_CW, 0x0F, 3, TStat{Follower: -1, Leader: -1})
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src == "" {
		t.Fatalf("decompile refused: %#v", diagnostics)
	}
	back, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatal(err)
	}
	r := NewEngine()
	r.Headless = true
	r.World = back
	r.BoardOpen(0)
	if r.GetStatIdAt(5, 5) < 0 {
		t.Fatalf("the conveyor at 5,5 lost its stat in decompile (diagnostics %#v)", diagnostics)
	}
}

// M36.27e: two boards with one name both stay reachable. The compiler binds a
// name to its first board, so a link to the second came back pointing at the
// first after decompile -> compile.
func TestM3627DecompileKeepsLinksToDuplicateBoardNames(t *testing.T) {
	world, err := CompileZWDWorld(zwdTwoRoomsExample)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	e.Headless = true
	e.World = world
	// Append a copy of board 1 as board 2, under board 1's name, and point
	// board 1's east exit at it.
	e.World.BoardCount++
	last := e.World.BoardCount
	e.World.BoardData[last] = append([]byte(nil), e.World.BoardData[1]...)
	e.World.BoardLen[last] = e.World.BoardLen[1]
	e.BoardOpen(1)
	firstName := e.Board.Name
	e.Board.Info.NeighborBoards[3] = byte(last) // east
	e.BoardClose()
	e.BoardOpen(last)
	e.Board.Name = firstName
	e.BoardClose()

	src, diagnostics := DecompileZWDAuthorable(&e.World)
	if src == "" {
		t.Fatalf("decompile refused: %#v", diagnostics)
	}
	back, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatalf("recompile: %v", err)
	}
	r := NewEngine()
	r.Headless = true
	r.World = back
	r.BoardOpen(1)
	if got := r.Board.Info.NeighborBoards[3]; got != byte(last) {
		t.Fatalf("board 1's east exit leads to board %d after the round trip, want %d", got, last)
	}
}

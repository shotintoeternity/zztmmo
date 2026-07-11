package zztgo

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"testing/quick"
)

func TestZWDExamplesCompileAndValidate(t *testing.T) {
	for name, src := range zwdExamples(t) {
		t.Run(name, func(t *testing.T) {
			data, err := CompileZWD(src)
			if err != nil {
				t.Fatalf("CompileZWD failed: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("CompileZWD returned no bytes")
			}
			validateCompiledZWD(t, data)
		})
	}
}

func TestZWDRejectsBadGridWithPreciseError(t *testing.T) {
	src := strings.Replace(zwdOneRoomExample, strings.Repeat(".", 58), strings.Repeat(".", 59), 1)
	_, err := CompileZWD(src)
	if err == nil {
		t.Fatal("CompileZWD succeeded for too-wide grid row")
	}
	if !strings.Contains(err.Error(), "grid row wider than 60") {
		t.Fatalf("error = %q, want grid width detail", err.Error())
	}
}

func TestZWDRejectsUnknownElement(t *testing.T) {
	src := strings.Replace(zwdOneRoomExample, "o = Object color 0x0F", "o = Goblet color 0x0F", 1)
	_, err := CompileZWD(src)
	if err == nil {
		t.Fatal("CompileZWD succeeded for unknown element")
	}
	if !strings.Contains(err.Error(), "unknown element") {
		t.Fatalf("error = %q, want unknown element detail", err.Error())
	}
}

func TestZWDRejectsTraversalAsWorldNameLengthOrCharsDoNotMatterToCompiler(t *testing.T) {
	src := strings.Replace(zwdOneRoomExample, `world "HELLO"`, `world "THIS-NAME-IS-MUCH-TOO-LONG"`, 1)
	_, err := CompileZWD(src)
	if err == nil {
		t.Fatal("CompileZWD succeeded for too-long world name")
	}
	if !strings.Contains(err.Error(), "world name must be 1..20 bytes") {
		t.Fatalf("error = %q, want world-name limit", err.Error())
	}
}

func TestZWDNoPanicOnMalformedInputs(t *testing.T) {
	cases := []string{
		"",
		"zwd 1\n",
		"zwd 1\nworld \"X\"\nboard \"B\"\nend\n",
		"zwd 1\nworld \"X\"\nboard \"B\"\n  grid\n" + strings.Repeat(".", 61) + "\n  end\nend\n",
		strings.Repeat("stat ", 1000),
		"zwd 1\nworld \"X\"\nboard \"B\"\n  start player at 1,1\n  grid\nend\n",
	}
	for _, src := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("CompileZWD panicked for %q: %v", src, r)
				}
			}()
			_, _ = CompileZWD(src)
		}()
	}
}

func TestZWDNoPanicQuick(t *testing.T) {
	err := quick.Check(func(src string) bool {
		defer func() {
			_ = recover()
		}()
		_, _ = CompileZWD(src)
		return true
	}, &quick.Config{MaxCount: 200})
	if err != nil {
		t.Fatal(err)
	}
}

func validateCompiledZWD(t *testing.T, data []byte) {
	t.Helper()
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	if err := e.worldReadFrom(bytes.NewReader(data), false, nil); err != nil {
		t.Fatalf("compiled bytes did not load: %v", err)
	}
	e.BoardOpen(0)
	e.BoardEnter(0)
	e.GameStateElement = E_PLAYER
	e.PlayerFor(0).Paused = false
	e.GamePlayExitRequested = false
	e.SetInputSource(&ScriptedInput{})
	for i := 0; i < 200; i++ {
		e.GameStep(nil)
		if e.GamePlayExitRequested {
			t.Fatalf("compiled world requested exit at step %d", i+1)
		}
	}
}

func zwdExamples(t *testing.T) map[string]string {
	t.Helper()
	doc, err := os.ReadFile("../ZWD.md")
	if err != nil {
		t.Fatalf("read ZWD.md: %v", err)
	}
	examples := extractZWDExampleFences(string(doc))
	if len(examples) != 2 {
		t.Fatalf("found %d ZWD examples in ZWD.md, want 2", len(examples))
	}
	return examples
}

func extractZWDExampleFences(doc string) map[string]string {
	examples := make(map[string]string)
	lines := strings.Split(doc, "\n")
	var title string
	for i := 0; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## Example: ") {
			title = strings.TrimPrefix(lines[i], "## Example: ")
			continue
		}
		if title == "" || strings.TrimSpace(lines[i]) != "```zwd" {
			continue
		}
		var b strings.Builder
		for i++; i < len(lines) && strings.TrimSpace(lines[i]) != "```"; i++ {
			b.WriteString(lines[i])
			b.WriteByte('\n')
		}
		examples[title] = b.String()
		title = ""
	}
	return examples
}

const zwdOneRoomExample = `zwd 1
world "HELLO"

board "Title screen"
  start player at 30,12
  max-shots 255
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................o.............................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    o = Object color 0x0F
  end

  stats
    stat at 30,11 element Object cycle 3 p1 cp437:0x02 under Empty color 0x00
    oop
@hello
"This world was written as text."
"The compiler turns it into real ZZT."
#end
    end
  end
end
`

const zwdTwoRoomsExample = `zwd 1
world "TWOROOMS"

board "Title screen"
  start player at 30,19
  max-shots 4
  dark false
  reenter false
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................p.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    p = Passage color 0x2F to "Vault"
  end

  stats
    stat at 30,12 element Passage cycle 0 p3 board "Vault" under Empty color 0x00
  end
end

board "Vault"
  start player at 30,19
  max-shots 4
  dark true
  reenter true
  time-limit 0
  exits north none south none west none east none

  grid
############################################################
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#.........................g.g.g............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................!.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#............................@.............................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
#..........................................................#
############################################################
  end

  legend
    # = Solid color 0x0E
    . = Empty color 0x0F
    @ = Player color 0x1F under Empty color 0x00
    g = Gem color yellow
    ! = Scroll color 0x0F
  end

  stats
    stat at 30,12 element Scroll cycle 1 under Empty color 0x00
    oop
You found the vault.
Bring a torch next time.
#end
    end
  end
end
`

func TestZWDOOPIndentationStripping(t *testing.T) {
	src := strings.Replace(zwdOneRoomExample, `    stat at 30,11 element Object cycle 3 p1 cp437:0x02 under Empty color 0x00
    oop
@hello
"This world was written as text."
"The compiler turns it into real ZZT."
#end
    end`, `    stat at 30,11 element Object cycle 3 p1 cp437:0x02 under Empty color 0x00
      oop
      @hello
      "This world was written as text."
      "The compiler turns it into real ZZT."
      #end
    end`, 1)

	world, err := CompileZWDWorld(src)
	if err != nil {
		t.Fatalf("CompileZWDWorld failed: %v", err)
	}

	e := NewEngine()
	e.World = world
	e.BoardOpen(0)

	if e.Board.StatCount < 1 {
		t.Fatalf("expected at least 1 stat (player)")
	}

	var objStat *TStat
	for i := int16(1); i <= e.Board.StatCount; i++ {
		stat := &e.Board.Stats[i]
		if stat.Under.Element == E_OBJECT || e.Board.Tiles[stat.X][stat.Y].Element == E_OBJECT {
			objStat = stat
			break
		}
	}

	if objStat == nil {
		t.Fatalf("could not find object stat on board")
	}

	expectedCode := "@hello\r\"This world was written as text.\"\r\"The compiler turns it into real ZZT.\"\r#end"
	actualCode := string(objStat.Data)
	if actualCode != expectedCode {
		t.Fatalf("OOP code was not stripped correctly:\nexpected: %q\nactual:   %q", expectedCode, actualCode)
	}
}




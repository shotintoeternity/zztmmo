package zztgo

import "testing"

func TestOrphanStatBackedDrawProcsDoNotPanic(t *testing.T) {
	e := NewEngine()
	e.Headless = true
	e.VideoInstall()
	e.WorldCreate()

	cases := []struct {
		name    string
		element byte
		x       int16
		y       int16
	}{
		{name: "bomb", element: E_BOMB, x: 5, y: 5},
		{name: "duplicator", element: E_DUPLICATOR, x: 6, y: 5},
		{name: "transporter", element: E_TRANSPORTER, x: 7, y: 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e.Board.Tiles[tc.x][tc.y] = TTile{Element: tc.element, Color: ElementDefs[tc.element].Color}
			if statID := e.GetStatIdAt(tc.x, tc.y); statID != -1 {
				t.Fatalf("test tile unexpectedly has stat %d", statID)
			}
			color, char := e.TileToColorAndChar(tc.x, tc.y)
			if char != ElementDefs[tc.element].Character {
				t.Fatalf("char=%d, want default %d", char, ElementDefs[tc.element].Character)
			}
			if color != ElementDefs[tc.element].Color {
				t.Fatalf("color=%#x, want %#x", color, ElementDefs[tc.element].Color)
			}
		})
	}
}

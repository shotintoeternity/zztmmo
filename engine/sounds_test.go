package zztgo

import "testing"

func TestSoundDrumTableFrozenForBrowser(t *testing.T) {
	want := []struct {
		len  int16
		data []uint16
	}{
		{1, []uint16{3200}},
		{14, []uint16{1100, 1200, 1300, 1400, 1500, 1600, 1700, 1800, 1900, 2000, 2100, 2200, 2300, 2400}},
		{14, []uint16{4800, 4800, 8000, 1600, 4800, 4800, 8000, 1600, 4800, 4800, 8000, 1600, 4800, 4800}},
		{14, []uint16{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
		{14, []uint16{500, 656, 4805, 1512, 1864, 3858, 2093, 1308, 2361, 2628, 910, 2873, 852, 4704}},
		{14, []uint16{1600, 895, 1600, 1269, 1600, 2267, 1600, 1388, 1600, 2039, 1600, 1324, 1600, 1916}},
		{14, []uint16{2200, 1760, 1760, 1320, 2640, 880, 2200, 1760, 1760, 1320, 2640, 880, 2200, 1760}},
		{14, []uint16{688, 676, 664, 652, 640, 628, 616, 604, 592, 580, 568, 556, 544, 532}},
		{14, []uint16{1192, 1216, 1241, 1228, 1207, 1261, 1109, 1271, 1207, 1341, 1036, 1303, 1059, 934}},
		{14, []uint16{436, 610, 583, 228, 282, 283, 440, 229, 480, 224, 560, 506, 559, 531}},
	}

	if len(SoundDrumTable) != len(want) {
		t.Fatalf("drum table len=%d, want %d", len(SoundDrumTable), len(want))
	}
	for i, drum := range SoundDrumTable {
		if drum.Len != want[i].len {
			t.Fatalf("drum %d len=%d, want %d", i, drum.Len, want[i].len)
		}
		for j, freq := range want[i].data {
			if got := drum.Data[j]; got != freq {
				t.Fatalf("drum %d data[%d]=%d, want %d", i, j, got, freq)
			}
		}
	}
}

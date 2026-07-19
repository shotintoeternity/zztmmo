/*
 * frontend_oracle.c — a deterministic, headless ZZT oracle harness for ZZTMMO
 * M16.2. Loads the real ZZT.EXE under Zeta's 8086 emulation core, boots into a
 * world, drives a scripted keyboard schedule over a *virtual* clock (so the run
 * is reproducible and the Turbo Pascal Randomize seed is fixed), and emits
 * checkpoints of the 80x25 text page read straight out of the emulated VRAM at
 * 0xB8000, plus every PC-speaker on/off event between checkpoints.
 *
 * The emulation authority is the real ZZT.EXE; this file is only the harness.
 * It intentionally reads state that ZZTMMO never produced — the whole point of
 * an independent oracle (M16.2 forbids self-comparison).
 *
 * Build: see oracle/build.sh (compiles this against a pinned Zeta checkout).
 *
 * Inputs:
 *   argv               the world file to load (parsed by posix_zzt_init), plus -t.
 *   $ZZT_ORACLE_SCN    path to the shared line-based scenario script.
 *   $ZZT_ORACLE_OUT    path to write the capture (default: stdout).
 *
 * Scenario directives (shared with the Go adapter in engine/oracle_parity_test.go;
 * unknown lines are errors):
 *   # ...            comment
 *   seed N           record only (RNG-free scenarios); timer offset is fixed
 *   boot N           run N PIT ticks to settle the title screen
 *   play             press P to enter play, then settle
 *   settle N         run N PIT ticks
 *   move DIR         press+release a direction (up/down/left/right), then settle
 *   key CH SC        press+release a raw key: decimal char, hex scancode
 *   capture LABEL    emit a checkpoint of the 80x25 text page
 *
 * Capture format (consumed by the Go adapter):
 *   sound on FREQ @MS / sound off @MS   speaker transitions since the previous
 *                                       checkpoint, in emulation order
 *   checkpoint LABEL                    header, then 25 rows of 320 hex chars
 *                                       (char,attr per cell, columns 0..79)
 *
 * Modeled on frontend_headless.c and the SDL frontend's execute/mark_timer loop.
 */
#define _DARWIN_C_SOURCE
#include <unistd.h>
#include <time.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "zzt.h"
#include "posix_vfs.h"

/* ---- virtual clock: the source of determinism --------------------------- */
static long g_vtime_ms = 0;
long zeta_time_ms(void) { return g_vtime_ms; }

static FILE *g_out = NULL;

/* ---- frontend stubs (no video/charset presentation needed) -------------- */
void cpu_ext_log(const char *s) {}
void speaker_on(int cycles, double freq) {
	if (g_out) fprintf(g_out, "sound on %d @%ld\n", (int) (freq + 0.5), g_vtime_ms);
}
void speaker_off(int cycles) {
	if (g_out) fprintf(g_out, "sound off @%ld\n", g_vtime_ms);
}
int zeta_has_feature(int feature) { return 1; }
void zeta_update_charset(int width, int height, u8 *data) {}
void zeta_update_palette(u32 *data) {}
void zeta_update_blink(int blink) {}
void zeta_show_developer_warning(const char *format, ...) {}

#include "asset_loader.h"
#include "frontend_posix.c"

#define CAP_W 80
#define CAP_H 25

/* Advance ZZT by up to `ticks` PIT ticks, running the CPU to idle between each.
 * Returns 0 if ZZT exited (STATE_END), else `ticks`. */
static int drive_ticks(int ticks) {
	double pit_ms = zzt_get_pit_tick_ms();
	for (int i = 0; i < ticks; i++) {
		int rcode, guard = 0;
		while ((rcode = zzt_execute(64000)) == STATE_CONTINUE) {
			if (++guard > 8192) break;
		}
		if (rcode == STATE_END) return 0;
		zzt_mark_frame();
		zzt_mark_timer();
		g_vtime_ms += (long) pit_ms;
	}
	return ticks;
}

static void press_key(int ch, int sc) {
	zzt_key(ch, sc);
	zzt_keyup(sc);
}

static int dir_scancode(const char *dir) {
	if (!strcmp(dir, "up")) return 0x48;
	if (!strcmp(dir, "down")) return 0x50;
	if (!strcmp(dir, "left")) return 0x4B;
	if (!strcmp(dir, "right")) return 0x4D;
	return -1;
}

/* Emit one checkpoint: 25 lines of 320 hex chars (char,attr per cell, cols
 * 0..79), preceded by a "checkpoint LABEL" header line. */
static void emit_checkpoint(const char *label) {
	u8 *ram = zzt_get_ram();
	fprintf(g_out, "checkpoint %s\n", label);
	for (int y = 0; y < CAP_H; y++) {
		for (int x = 0; x < CAP_W; x++) {
			long addr = 0xB8000 + (y * 160) + (x * 2);
			fprintf(g_out, "%02x%02x", ram[addr], ram[addr + 1]);
		}
		fputc('\n', g_out);
	}
}

static int settle_default = 8;

static void run_scenario(const char *path) {
	FILE *f = fopen(path, "r");
	if (!f) { fprintf(stderr, "oracle: cannot open scenario %s\n", path); exit(3); }

	char line[512];
	while (fgets(line, sizeof(line), f)) {
		char *nl = strpbrk(line, "\r\n");
		if (nl) *nl = 0;
		char *p = line;
		while (*p == ' ' || *p == '\t') p++;
		if (*p == 0 || *p == '#') continue;

		char cmd[64], a1[128], a2[128];
		int n = sscanf(p, "%63s %127s %127s", cmd, a1, a2);
		if (n < 1) continue;

		if (!strcmp(cmd, "seed")) {
			/* recorded by the caller; RNG-free scenarios don't depend on it */
		} else if (!strcmp(cmd, "boot")) {
			if (drive_ticks(atoi(a1)) == 0) { fprintf(stderr, "oracle: exit during boot\n"); exit(4); }
		} else if (!strcmp(cmd, "play")) {
			press_key('p', 0x19);
			drive_ticks(30);
		} else if (!strcmp(cmd, "settle")) {
			drive_ticks(atoi(a1));
		} else if (!strcmp(cmd, "move")) {
			int sc = dir_scancode(a1);
			if (sc < 0) { fprintf(stderr, "oracle: bad direction %s\n", a1); exit(5); }
			press_key(0, sc);
			drive_ticks(settle_default);
		} else if (!strcmp(cmd, "key")) {
			press_key(atoi(a1), (int) strtol(a2, NULL, 16));
			drive_ticks(settle_default);
		} else if (!strcmp(cmd, "capture")) {
			emit_checkpoint(a1);
		} else {
			fprintf(stderr, "oracle: unknown scenario directive '%s'\n", cmd);
			exit(6);
		}
	}
	fclose(f);
}

int main(int argc, char **argv) {
	if (posix_zzt_init(argc, argv) < 0) {
		fprintf(stderr, "oracle: could not load ZZT!\n");
		return 1;
	}
	/* Determinism: override the wall-clock timer offset posix_zzt_init set. */
	zzt_set_timer_offset(0);
	g_vtime_ms = 0;

	const char *scn = getenv("ZZT_ORACLE_SCN");
	if (!scn) { fprintf(stderr, "oracle: set ZZT_ORACLE_SCN\n"); return 2; }
	const char *outp = getenv("ZZT_ORACLE_OUT");
	g_out = outp ? fopen(outp, "w") : stdout;
	if (!g_out) { fprintf(stderr, "oracle: cannot open output %s\n", outp); return 2; }

	run_scenario(scn);

	if (g_out != stdout) fclose(g_out);
	return 0;
}

/* Build configuration for the oracle build of Zeta (normally meson-generated;
 * hand-pinned here so oracle/build.sh can compile without meson). */
#define VERSION "zeta-oracle"

#define HAVE_FTRUNCATE
#define HAVE_OPENDIR

#define UNALIGNED_OK

#define RESAMPLE_LINEAR

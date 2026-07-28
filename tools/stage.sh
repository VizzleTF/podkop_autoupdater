#!/bin/sh
# Lay the built binaries out as owfeed expects: one root filesystem per OpenWrt
# architecture, under dist/root/<arch>.
#
# The mapping from GOARCH to OpenWrt architectures lives here, next to the build
# that produces the binaries. It used to live in the community feed, which meant the
# feed also had to carry a copy of scripts/init.d/podkop_updater and keep it in step
# with this repository by hand.
#
# Usage: tools/stage.sh <version>   (with go/dist/podkop_updater-* already built)
set -eu

VERSION="${1:?usage: tools/stage.sh <version>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${SRC:-$ROOT/go/dist}"
OUT="${OUT:-$ROOT/dist}"

# <build target> <the OpenWrt architectures it runs on>
#
# A static binary needs the right target and no OpenWrt SDK, so one build serves
# every architecture that shares its GOARCH.
TARGETS="
amd64  x86_64
arm64  aarch64_cortex-a53 aarch64_cortex-a72 aarch64_cortex-a76 aarch64_generic
armv7  arm_cortex-a5_vfpv4 arm_cortex-a7 arm_cortex-a7_neon-vfpv4 arm_cortex-a7_vfpv4 arm_cortex-a8_vfpv3 arm_cortex-a9 arm_cortex-a9_neon arm_cortex-a9_vfpv3-d16 arm_cortex-a15_neon-vfpv4
mips   mips_24kc mips_mips32
mipsle mipsel_24kc mipsel_24kc_24kf mipsel_74kc mipsel_mips32
"

rm -rf "$OUT/root"
mkdir -p "$OUT"
printf '%s\n' "$VERSION" > "$OUT/version"

echo "$TARGETS" | while read -r target arches; do
	[ -n "$target" ] || continue
	bin="$SRC/podkop_updater-$target"
	# A target that did not build is not a package built without it. The release
	# would otherwise be missing architectures with nothing said about which.
	[ -f "$bin" ] || { echo "$bin is missing; build it before staging" >&2; exit 1; }

	for arch in $arches; do
		r="$OUT/root/$arch"
		mkdir -p "$r/usr/bin" "$r/etc/init.d"
		install -m 0755 "$bin" "$r/usr/bin/podkop_updater"
		install -m 0755 "$ROOT/go/scripts/init.d/podkop_updater" "$r/etc/init.d/podkop_updater"
	done
	echo ">> $target -> $(echo "$arches" | wc -w | tr -d ' ') architecture(s)"
done

# Source from Git Bash on the Windows development host:
#   . rust/tools/env.sh
# Puts rustup, a MinGW-w64 GCC (WinLibs, for bundled C code such as SQLite and
# for dlltool) and zig (Linux cross-linking) on PATH.
mingw="$(ls -d /c/Users/*/AppData/Local/Microsoft/WinGet/Packages/BrechtSanders.WinLibs*/mingw64/bin 2>/dev/null | head -1)"
zigdir="$(ls -d /c/Users/*/AppData/Local/Microsoft/WinGet/Packages/zig.zig*/zig-* 2>/dev/null | head -1)"
export PATH="$HOME/.cargo/bin:${mingw}:${zigdir}:$PATH"

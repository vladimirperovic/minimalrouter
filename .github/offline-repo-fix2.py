import runpy
from pathlib import Path

try:
    runpy.run_path('.github/offline-repo-fix.py', run_name='__main__')
except SystemExit as exc:
    if 'operator has already explicitly typed ERASE' not in str(exc):
        raise

p = Path('packaging/alpine/live-installer.sh')
s = p.read_text()
marker = '''# The target has either passed the conservative virtual-disk guard or the
# operator explicitly confirmed it. Capture Alpine's verbose installer output:
# if setup-disk ever fails, the ISO/CI log must contain the actual reason rather
# than only a generic MinimalRouter error.
SETUP_DISK_LOG=/tmp/minimalrouter-setup-disk.log
'''
replacement = '''# Reassert the local ISO repository at the last possible point. This is also
# what makes the CI full-install test a genuine zero-Internet installation.
restore_alpine_media_repo

# The target has either passed the conservative virtual-disk guard or the
# operator explicitly confirmed it. Capture Alpine's verbose installer output:
# if setup-disk ever fails, the ISO/CI log must contain the actual reason rather
# than only a generic MinimalRouter error.
SETUP_DISK_LOG=/tmp/minimalrouter-setup-disk.log
'''
if s.count(marker) != 1:
    raise SystemExit(f'live-installer: expected current setup-disk marker once, found {s.count(marker)}')
p.write_text(s.replace(marker, replacement, 1))

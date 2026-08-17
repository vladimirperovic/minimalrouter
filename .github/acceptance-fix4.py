from pathlib import Path

# Apply the full acceptance patch first.
exec(compile(Path('.github/acceptance-fix3.py').read_text(), '.github/acceptance-fix3.py', 'exec'))

# The marker-based prose replacement intentionally stops at the original blank
# line before the PPPoE prompt. Keep the original PPPoE assignment exactly once.
p = Path('cmd/router-setup/main.go')
s = p.read_text()
bad = '\tpppoeUser, err :=\tfmt.Println()\n\n\tpppoeUser, err :='
if s.count(bad) != 1:
    raise SystemExit(f'cmd/router-setup/main.go: expected one duplicated PPPoE assignment, found {s.count(bad)}')
p.write_text(s.replace(bad, '\tpppoeUser, err :=', 1))

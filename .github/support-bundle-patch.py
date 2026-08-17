from pathlib import Path

p = Path('cmd/router-recovery/main.go')
s = p.read_text()

def one(old, new):
    global s
    n = s.count(old)
    if n != 1:
        raise SystemExit(f'expected one match, got {n}: {old[:100]!r}')
    s = s.replace(old, new, 1)

one('''\tcase "snapshots":
\t\tlistSnapshots()
''', '''\tcase "snapshots":
\t\tlistSnapshots()
\tcase "support-bundle":
\t\tfs := flag.NewFlagSet("support-bundle", flag.ExitOnError)
\t\toutput := fs.String("output", "", "output .tar.gz path (default: /tmp/minimalrouter-support-<timestamp>.tar.gz)")
\t\t_ = fs.Parse(args)
\t\tpath, err := createSupportBundle(*output)
\t\tif err != nil {
\t\t\tfatal(err)
\t\t}
\t\tfmt.Println("Sanitized support bundle created:", path)
''')

one('''\t\tfmt.Print("\\nMinimal Router Recovery Console\\n===============================\\n1) Show interfaces / status\\n2) Assign WAN interface\\n3) Assign LAN interface + IP\\n4) Restore last-known-good configuration\\n5) List / restore snapshot\\n6) Factory reset\\n7) Reset admin password / TOTP\\n8) Restart router services\\n9) Reboot\\n0) Shell\\nq) Quit\\n\\nSelect: ")
''', '''\t\tfmt.Print("\\nminimalrouter recovery\\n======================\\n1) Show interfaces / status\\n2) Assign WAN interface\\n3) Assign LAN interface + IP\\n4) Restore last-known-good configuration\\n5) List / restore snapshot\\n6) Factory reset\\n7) Reset admin password / TOTP\\n8) Restart router services\\n9) Reboot\\ns) Create sanitized support bundle\\n0) Shell\\nq) Quit\\n\\nSelect: ")
''')

one('''\t\tcase "9":
\t\t\tif askConfirm("Reboot this router VM now?") {
\t\t\t\trun("reboot")
\t\t\t}
\t\tcase "0":
''', '''\t\tcase "9":
\t\t\tif askConfirm("Reboot this router VM now?") {
\t\t\t\trun("reboot")
\t\t\t}
\t\tcase "s", "S":
\t\t\tpath, err := createSupportBundle("")
\t\t\tif err != nil {
\t\t\t\tfmt.Println("ERROR:", err)
\t\t\t} else {
\t\t\t\tfmt.Println("Sanitized support bundle created:", path)
\t\t\t\tfmt.Println("It excludes passwords, private keys, configuration DB, process environments and shell history.")
\t\t\t}
\t\tcase "0":
''')

one('''  snapshots
  restore-last-good
''', '''  snapshots
  support-bundle [--output PATH]
  restore-last-good
''')

p.write_text(s)

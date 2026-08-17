from pathlib import Path


def replace_once(path, old, new):
    p = Path(path)
    s = p.read_text()
    n = s.count(old)
    if n != 1:
        raise SystemExit(f"{path}: expected one match, got {n}: {old[:100]!r}")
    p.write_text(s.replace(old, new, 1))

p = "cmd/router-setup/main.go"

# Move PPPoE credentials until after WAN/LAN roles are understood.
old_creds = '''\tpppoeUser, err := ui.readLine("PPPoE username (leave empty for an isolated lab): ")
\tif err != nil {
\t\treturn err
\t}
\tpppoePass := ""
\tif pppoeUser != "" {
\t\tpppoePass, err = ui.readSecret("PPPoE password: ")
\t\tif err != nil {
\t\t\treturn err
\t\t}
\t\tif pppoePass == "" {
\t\t\treturn errors.New("PPPoE password cannot be empty when a username is supplied")
\t\t}
\t}

'''
replace_once(p, old_creds, '')

old_confirm = '''\tfmt.Printf("Reason: %s\\n", reason)

\tuseSuggested, err := ui.confirm("Use these WAN/LAN roles?", true)
'''
new_confirm = '''\tfmt.Printf("Reason: %s\\n", reason)
\tpppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
\tif pppoeBased {
\t\tfmt.Println()
\t\tui.warn("PPPoE discovery only detects that a service answered; it does not start a PPPoE session.")
\t\tui.warn("An existing router or shared upstream can make PPPoE visible during migration.")
\t\tfmt.Println("Please explicitly confirm the WAN/LAN roles; pressing Enter alone will NOT accept this PPPoE-based suggestion.")
\t}

\tuseSuggested, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
'''
replace_once(p, old_confirm, new_confirm)

marker = '''\tif wan == lan || wan == "" || lan == "" {
\t\treturn errors.New("WAN and LAN must be two different interfaces")
\t}

\tadminPass := ""
'''
creds_after_roles = '''\tif wan == lan || wan == "" || lan == "" {
\t\treturn errors.New("WAN and LAN must be two different interfaces")
\t}

\tfmt.Println()
\tfmt.Printf("WAN is %s; LAN is %s.\\n", wan, lan)
\tfmt.Println("PPPoE credentials are optional during installation. Leave the username empty to configure them later in the Dashboard.")
\tpppoeUser, err := ui.readLine("PPPoE username [skip]: ")
\tif err != nil {
\t\treturn err
\t}
\tpppoePass := ""
\tif pppoeUser != "" {
\t\tpppoePass, err = ui.readSecret("PPPoE password: ")
\t\tif err != nil {
\t\t\treturn err
\t\t}
\t\tif pppoePass == "" {
\t\t\treturn errors.New("PPPoE password cannot be empty when a username is supplied")
\t\t}
\t}

\tadminPass := ""
'''
replace_once(p, marker, creds_after_roles)

# Update the concise setup explanation to match the safer order.
replace_once(p,
'''\tfmt.Println("  1. PPPoE — press Enter to skip it if you do not want to configure it now")
\tfmt.Println("  2. WAN/LAN — the installer suggests the likely ports; press Enter if correct")
\tfmt.Println("  3. Dashboard password — required, minimum 12 characters")
\tfmt.Println("  4. A final review before the configuration is saved")''',
'''\tfmt.Println("  1. WAN/LAN — the installer suggests likely ports and explains why")
\tfmt.Println("  2. PPPoE — optional; press Enter to skip it and configure it later")
\tfmt.Println("  3. Dashboard password — required, minimum 12 characters")
\tfmt.Println("  4. A final review before the configuration is saved")''')

# Add focused policy helper tests through the reason-derived default behavior.
p = "cmd/router-setup/main_test.go"
s = Path(p).read_text()
if "TestPPPoEBasedSuggestionRequiresExplicitConfirmation" not in s:
    s += r'''

func TestPPPoEBasedSuggestionRequiresExplicitConfirmation(t *testing.T) {
    for _, reason := range []string{
        "only this interface answered PPPoE discovery",
        "PPPoE answered on multiple interfaces; manual confirmation is required",
    } {
        pppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
        if !pppoeBased {
            t.Fatalf("reason %q must be treated as requiring explicit confirmation", reason)
        }
        ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
        got, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
        if err != nil { t.Fatal(err) }
        if got { t.Fatalf("Enter must not accept PPPoE-based suggestion for %q", reason) }
    }
}

func TestNonPPPoESuggestionStillAllowsSafeEnterDefault(t *testing.T) {
    reason := "no PPPoE discovery response; falling back to link/default-route hardware scoring"
    pppoeBased := reason == "only this interface answered PPPoE discovery" || strings.HasPrefix(reason, "PPPoE answered on multiple interfaces")
    ui := &consoleUI{reader: bufio.NewReader(strings.NewReader("\n"))}
    got, err := ui.confirm("Use these WAN/LAN roles?", !pppoeBased)
    if err != nil { t.Fatal(err) }
    if !got { t.Fatal("Enter should accept a non-PPPoE suggestion when heuristic roles are otherwise valid") }
}
'''
    Path(p).write_text(s)

# Update the full-install harness for the new prompt order. Its offline QEMU has
# no PPPoE responder, so the heuristic recommendation remains Enter-safe.
p = "scripts/ci/iso-full-install.exp"
replace_once(p,
'''# The first visible application screen is the MinimalRouter ASCII welcome and
# the first actual question is PPPoE. There is no extra Enter gate before setup.
wait_and_send {PPPoE username} "\\r"
wait_and_send {Use these WAN/LAN roles} "\\r"
wait_and_send {administrator password.*12 characters} "$dashboard_password\\r"
''',
'''# The first visible application screen is the MinimalRouter ASCII welcome.
# Network roles are understood before optional ISP credentials are requested.
wait_and_send {Use these WAN/LAN roles} "\\r"
wait_and_send {PPPoE username.*skip} "\\r"
wait_and_send {administrator password.*12 characters} "$dashboard_password\\r"
''')

$ErrorActionPreference = 'Stop'

$user = 'sshbot'
$pass = 'Osman!Home#2026'
$secure = ConvertTo-SecureString $pass -AsPlainText -Force

Write-Host '== 1/5 Instaliram OpenSSH Server ==' -ForegroundColor Cyan
$cap = Get-WindowsCapability -Online -Name OpenSSH.Server* | Select-Object -First 1
if ($cap.State -ne 'Installed') {
    Add-WindowsCapability -Online -Name $cap.Name | Out-Null
}
Write-Host '  OK' -ForegroundColor Green

Write-Host '== 2/5 Namestam servis (auto start) ==' -ForegroundColor Cyan
Set-Service sshd -StartupType Automatic
Start-Service sshd
Write-Host '  OK' -ForegroundColor Green

Write-Host '== 3/5 Firewall (port 22) ==' -ForegroundColor Cyan
New-NetFirewallRule -Name sshd -DisplayName 'OpenSSH Server' -Enabled True -Direction Inbound -Protocol TCP -Action Allow -LocalPort 22 -ErrorAction SilentlyContinue
Write-Host '  OK' -ForegroundColor Green

Write-Host '== 4/5 Kreiranje SSH korisnika ==' -ForegroundColor Cyan
if (Get-LocalUser -Name $user -ErrorAction SilentlyContinue) {
    Set-LocalUser -Name $user -Password $secure
    Write-Host '  Korisnik postoji, resetovan pass.'
} else {
    New-LocalUser -Name $user -Password $secure -FullName 'SSH account' -Description 'Remote SSH only, no desktop'
    Add-LocalGroupMember -Group 'Users' -Member $user
    $deny = Get-LocalGroup -SID 'S-1-5-32-559'
    Add-LocalGroupMember -Group $deny -Member $user
    Write-Host '  Kreiran.'
}
Write-Host '  OK' -ForegroundColor Green

Write-Host '== 5/5 Zavrseno ==' -ForegroundColor Cyan
Write-Host ''
Write-Host ('  User     : ' + $user)
Write-Host ('  Lozinka  : ' + $pass)
Write-Host ('  Servis   : ' + (Get-Service sshd).Status)
Write-Host '  IP adrese:'
Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | ForEach-Object { Write-Host ('    ' + $_.IPAddress + '  (' + $_.InterfaceAlias + ')') }
Write-Host ''
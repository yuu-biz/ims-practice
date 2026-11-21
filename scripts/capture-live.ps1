# Streams the compose bridge into Wireshark on the host.
# Requires PowerShell 7.4 or later, which passes binary output between
# native commands unchanged.
param(
	[string]$Wireshark = (Join-Path $env:ProgramFiles 'Wireshark\Wireshark.exe')
)
Set-Location (Split-Path $PSScriptRoot -Parent)
docker compose exec -T capture tcpdump -i ims0 -U -n -w - | & $Wireshark -k -i -

# Falha (exit 1) se algum arquivo consumido no Linux contem CR (CRLF).
# Uso: powershell -File tools\check-lf.ps1
$root = Split-Path -Parent $PSScriptRoot
$skipDirs = '\.git|\.tools|dist|node_modules|vendor'
$exts  = '.sh', '.service', '.conf', '.yaml', '.yml', '.example', '.py', '.ini', '.csv', '.tmpl', '.socket', '.php', '.xml'
$names = 'Makefile', 'postinstall', 'preinstall', 'preremove', 'postremove', 'postupgrade'

$files = Get-ChildItem -LiteralPath $root -Recurse -File -Force | Where-Object {
    $rel = $_.FullName.Substring($root.Length)
    ($rel -notmatch "^[\\/]($skipDirs)[\\/]") -and
    (($exts -contains $_.Extension.ToLower()) -or ($names -contains $_.Name))
}

$bad = @()
foreach ($f in $files) {
    if ([Array]::IndexOf([IO.File]::ReadAllBytes($f.FullName), [byte]13) -ge 0) { $bad += $f.FullName }
}

if ($bad.Count -gt 0) {
    Write-Host "FALHA: CR (CRLF) encontrado em:"
    $bad | ForEach-Object { Write-Host "  $_" }
    exit 1
}
Write-Host "OK: $($files.Count) arquivos verificados, nenhum CR."
exit 0

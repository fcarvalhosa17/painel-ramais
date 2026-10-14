# Equivalente ao Makefile. Uso: .\build.ps1 [build|test|rpm|bundle|all] [-Version 0.1.1]
# Ferramentas: go e nfpm do PATH ou de .tools\ (go\bin\go.exe, nfpm.exe, nfpm\nfpm.exe).
param(
    [ValidateSet('build', 'test', 'rpm', 'bundle', 'all')][string]$Target = 'build',
    [string]$Version = '0.1.1'  # fonte unica: vai para o binario (-X main.version), o RPM (VERSION) e o bundle
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

# Caches do Go dentro da raiz (nada gravado em %USERPROFILE%).
$env:GOPATH = Join-Path $PSScriptRoot '.tools\gopath'
$env:GOCACHE = Join-Path $PSScriptRoot '.tools\gocache'
$env:GOMODCACHE = Join-Path $PSScriptRoot '.tools\gopath\pkg\mod'

function Find-Tool([string]$name, [string[]]$local, [string]$hint) {
    $c = Get-Command $name -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    foreach ($p in $local) {
        $full = Join-Path $PSScriptRoot $p
        if (Test-Path $full) { return $full }
    }
    throw "'$name' nao encontrado no PATH nem em .tools\. $hint"
}

function Invoke-Checked([string]$exe, [string[]]$argList) {
    & $exe @argList
    if ($LASTEXITCODE -ne 0) { throw "falhou (exit $LASTEXITCODE): $exe $($argList -join ' ')" }
}

function Build {
    $go = Find-Tool 'go' @('.tools\go\bin\go.exe') 'Instale o Go ou extraia em .tools\go.'
    New-Item -ItemType Directory -Force dist | Out-Null
    $old = @($env:GOOS, $env:GOARCH, $env:CGO_ENABLED)
    try {
        $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
        Invoke-Checked $go @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Version", '-o', 'dist/painel-ramais', './cmd/painel-ramais')
    }
    finally { $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $old }
    Write-Host 'OK: dist/painel-ramais (linux/amd64)'
}

function Test {
    $go = Find-Tool 'go' @('.tools\go\bin\go.exe') 'Instale o Go ou extraia em .tools\go.'
    Invoke-Checked $go @('test', './...')
}

function Rpm {
    $nfpm = Find-Tool 'nfpm' @('.tools\nfpm.exe', '.tools\nfpm\nfpm.exe') 'Baixe o nfpm (github.com/goreleaser/nfpm) para .tools\.'
    Invoke-Checked 'powershell' @('-NoProfile', '-File', 'tools\check-lf.ps1')
    Build
    $env:VERSION = $Version
    Invoke-Checked $nfpm @('pkg', '--packager', 'rpm', '--config', 'packaging/nfpm.yaml', '--target', 'dist/')
    Write-Host 'OK: RPM em dist/'
}

function Bundle {
    $py = Get-Command python -ErrorAction SilentlyContinue
    if (-not $py) { throw "'python' nao encontrado no PATH (necessario para tools/bundle.py)." }
    Invoke-Checked 'powershell' @('-NoProfile', '-File', 'tools\check-lf.ps1')
    Invoke-Checked $py.Source @('-I', 'tools/bundle.py', $Version)
}

try {
    switch ($Target) {
        'build' { Build }
        'test'  { Test }
        'rpm'   { Rpm }
        'bundle' { Rpm; Bundle }
        'all'   { Test; Rpm; Bundle }
    }
}
catch {
    Write-Host "ERRO: $($_.Exception.Message)"
    exit 1
}

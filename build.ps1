# Equivalente ao Makefile. Uso: .\build.ps1 [build|test|rpm|bundle|all|release] [-Version 0.1.1] [-Pre rc.N] [-DryRun]
# Pre-release: -Version 0.1.2 -Pre rc.1 (ou -Version 0.1.2-rc.1). Tag v0.1.2-rc.1; RPM Version 0.1.2, Release 0.1.rc.1
# (rpmvercmp: 0.1.2-1 final > 0.1.2-0.1.rc.1 > 0.1.1-1; sem til no nome do arquivo, que o GitHub reescreve). Release com --prerelease.
# release: gera RPM + tar.gz + dist/SHA256SUMS (so compila e empacota; nao roda go test), cria a tag v<Version> e publica a Release via gh. Com -DryRun nao cria tag nem Release.
# Ferramentas: go e nfpm do PATH ou de .tools\ (go\bin\go.exe, nfpm.exe, nfpm\nfpm.exe).
param(
    [ValidateSet('build', 'test', 'rpm', 'bundle', 'all', 'release')][string]$Target = 'build',
    [string]$Version = '0.1.1',  # fonte unica: vai para o binario (-X main.version), o RPM (VERSION) e o bundle
    [string]$Pre = '',           # sufixo de pre-release, formato rc.N (vazio = versao final)
    [switch]$DryRun              # so vale em release: roda as conferencias e o build, sem tag nem Release
)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

# -Version 0.1.2-rc.1 equivale a -Version 0.1.2 -Pre rc.1.
if ($Version -match '^(\d+\.\d+\.\d+)-(.+)$') {
    if ($Pre -and $Pre -ne $Matches[2]) { Write-Host "ERRO: -Version traz '-$($Matches[2])' e -Pre e '$Pre'."; exit 1 }
    $Version = $Matches[1]; $Pre = $Matches[2]
}
if ($Version -notmatch '^\d+\.\d+\.\d+$') { Write-Host "ERRO: -Version '$Version' invalida (esperado X.Y.Z)."; exit 1 }
if ($Pre -and $Pre -notmatch '^rc\.\d+$') { Write-Host "ERRO: -Pre '$Pre' invalido (esperado rc.N)."; exit 1 }
$Full = if ($Pre) { "$Version-$Pre" } else { $Version }   # tag, binario (-X main.version) e tar.gz
$Rel = if ($Pre) { "0.1.$Pre" } else { '1' }                 # Release do RPM

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
        Invoke-Checked $go @('build', '-trimpath', '-ldflags', "-s -w -X main.version=$Full", '-o', 'dist/painel-ramais', './cmd/painel-ramais')
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
    $env:RELEASE = $Rel
    Invoke-Checked $nfpm @('pkg', '--packager', 'rpm', '--config', 'packaging/nfpm.yaml', '--target', 'dist/')
    Write-Host 'OK: RPM em dist/'
}

function Bundle {
    $py = Get-Command python -ErrorAction SilentlyContinue
    if (-not $py) { throw "'python' nao encontrado no PATH (necessario para tools/bundle.py)." }
    Invoke-Checked 'powershell' @('-NoProfile', '-File', 'tools\check-lf.ps1')
    Invoke-Checked $py.Source @('-I', 'tools/bundle.py', $Full, '--rpm', "dist/painel-ramais-$Version-$Rel.x86_64.rpm")
}

function Get-Sha256([string]$path) {  # .NET direto: Get-FileHash falha quando o PSModulePath esta poluido
    $sha = [Security.Cryptography.SHA256]::Create()
    $fs = [IO.File]::OpenRead((Resolve-Path $path).Path)
    try { return ([BitConverter]::ToString($sha.ComputeHash($fs)) -replace '-', '').ToLower() } finally { $fs.Dispose(); $sha.Dispose() }
}

function Release {
    $tag = "v$Full"
    $falhas = New-Object System.Collections.Generic.List[string]
    # Em release real a primeira falha aborta; em -DryRun todas sao listadas e o exit final e 1.
    function Falha([string]$msg) {
        if (-not $DryRun) { throw $msg }
        Write-Host "[FALHA] $msg"; $falhas.Add($msg)
    }
    $git = Find-Tool 'git' @() 'Instale o Git.'
    $gh = Find-Tool 'gh' @() 'Instale o GitHub CLI (cli.github.com).'

    if (git status --porcelain) { Falha 'arvore de trabalho suja (commit ou descarte antes de publicar).' } else { Write-Host '[ok] arvore limpa' }
    & $git fetch --tags --quiet
    if ($LASTEXITCODE -ne 0) { Falha 'git fetch falhou.' }
    if (git tag -l $tag) { Falha "tag $tag ja existe localmente." }
    elseif (git ls-remote --tags origin "refs/tags/$tag") { Falha "tag $tag ja existe no origin." }
    else { Write-Host "[ok] tag $tag livre" }
    if (git rev-list 'origin/main..HEAD') { Falha 'HEAD tem commits que nao estao em origin/main (faca push antes).' } else { Write-Host '[ok] HEAD publicado em origin/main' }
    & $gh auth status *> $null
    if ($LASTEXITCODE -ne 0) { Falha 'gh nao autenticado (rode: gh auth login).' } else { Write-Host '[ok] gh autenticado' }

    Rpm; Bundle

    $rpm = "painel-ramais-$Version-$Rel.x86_64.rpm"
    $tgz = "painel-ramais-$Full.tar.gz"
    $linhas = foreach ($f in @($rpm, $tgz)) {
        $path = Join-Path 'dist' $f
        if (-not (Test-Path $path)) { throw "artefato ausente: $path" }
        '{0}  {1}' -f (Get-Sha256 $path), $f
    }
    [IO.File]::WriteAllText((Join-Path $PSScriptRoot 'dist\SHA256SUMS'), (($linhas -join "`n") + "`n"), (New-Object Text.UTF8Encoding $false))
    Write-Host 'SHA256SUMS:'; $linhas | ForEach-Object { Write-Host "  $_" }

    $anexos = @("dist/$rpm", "dist/$tgz", 'dist/SHA256SUMS')
    if ($Pre) {
        # Texto exato exigido; montado por codepoint para nao depender da codificacao deste arquivo.
        $aviso = "N$([char]0xC3)O HOMOLOGADA. Uso exclusivo em laborat$([char]0xF3)rio."
        $notas = Join-Path $PSScriptRoot 'dist\NOTAS-PRERELEASE.md'
        [IO.File]::WriteAllText($notas, "$aviso`n`nPre-release $Full. Confira os arquivos com: sha256sum -c SHA256SUMS`n", (New-Object Text.UTF8Encoding $false))
        $extra = @('--prerelease', '--latest=false', '--notes-file', 'dist/NOTAS-PRERELEASE.md')
        Write-Host "PRE-RELEASE: descricao = $aviso"
    }
    else { $extra = @('--notes', "Painel de Ramais $Version. Confira os arquivos com: sha256sum -c SHA256SUMS") }
    $ghArgs = @('release', 'create', $tag, '--verify-tag', '--title', "Painel de Ramais $Full") + $extra + $anexos
    if ($DryRun) {
        Write-Host 'DRY-RUN: nada criado. Faria:'
        Write-Host "  git tag -a $tag -m `"Painel de Ramais $Full`""
        Write-Host "  git push origin $tag"
        Write-Host "  gh $($ghArgs -join ' ')"
        if ($falhas.Count -gt 0) { throw "dry-run: $($falhas.Count) conferencia(s) falhariam no release real." }
        return
    }
    Invoke-Checked $git @('tag', '-a', $tag, '-m', "Painel de Ramais $Full")
    Invoke-Checked $git @('push', 'origin', $tag)
    Invoke-Checked $gh $ghArgs
    Write-Host "OK: Release $tag publicada."
}


try {
    switch ($Target) {
        'build' { Build }
        'test'  { Test }
        'rpm'   { Rpm }
        'bundle' { Rpm; Bundle }
        'all'   { Rpm; Bundle }
        'release' { Release }
    }
}
catch {
    Write-Host "ERRO: $($_.Exception.Message)"
    exit 1
}

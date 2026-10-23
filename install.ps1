<#
.SYNOPSIS
  Instala (ou remove) o simple-orchestrator e a skill local-agent-flow no Windows.

.DESCRIPTION
  Formas de uso:
    1) Comando de uma linha (baixa a ultima release do GitHub):
         irm https://raw.githubusercontent.com/jovemcleb/simple-orchestrator/main/install.ps1 | iex
    2) A partir de um zip de release extraido:   .\install.ps1
    3) A partir de um clone do repositorio (compila com Go):   .\install.ps1

  O binario vai para %LOCALAPPDATA%\Programs\simple-orchestrator e essa pasta e adicionada ao PATH do usuario.
  A skill e copiada para as pastas de skills do Codex e do Antigravity que existirem nesta maquina.
  Copias antigas da skill sao guardadas em %LOCALAPPDATA%\Programs\simple-orchestrator\backups.

.PARAMETER Uninstall
  Remove o binario, a entrada do PATH e a skill instalada. O estado das execucoes
  (%LOCALAPPDATA%\SimpleOrchestrator) so e removido com -Purge.

.PARAMETER Version
  Tag da release a baixar (ex.: v0.1.0). Padrao: a mais recente. Tambem aceita $env:SIMPLE_ORCHESTRATOR_VERSION.
#>
[CmdletBinding()]
param(
    [switch]$Uninstall,
    [switch]$Purge,
    [string]$Version = $env:SIMPLE_ORCHESTRATOR_VERSION
)

$ErrorActionPreference = 'Stop'
$Repo = 'jovemcleb/simple-orchestrator'
$ExeName = 'simple-orchestrator.exe'
$SkillName = 'local-agent-flow'
$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\simple-orchestrator'
$StateDir = Join-Path $env:LOCALAPPDATA 'SimpleOrchestrator'
$BackupDir = Join-Path $InstallDir 'backups'

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg) { Write-Host "  [ok] $msg" -ForegroundColor Green }
function Write-Warn2($msg) { Write-Host "  [!]  $msg" -ForegroundColor Yellow }

# Skill folders read by the supported hosts. Only folders whose host is installed are used.
function Get-SkillTargets {
    $h = $env:USERPROFILE
    $candidates = @(
        @{ Host = 'Codex';       Parent = (Join-Path $h '.codex');              Dir = (Join-Path $h '.codex\skills') },
        @{ Host = 'Agents';      Parent = (Join-Path $h '.agents');             Dir = (Join-Path $h '.agents\skills') },
        @{ Host = 'Antigravity'; Parent = (Join-Path $h '.gemini\antigravity'); Dir = (Join-Path $h '.gemini\antigravity\skills') },
        @{ Host = 'Gemini';      Parent = (Join-Path $h '.gemini');             Dir = (Join-Path $h '.gemini\skills') }
    )
    $candidates | Where-Object { Test-Path $_.Parent }
}

function Remove-FromUserPath($dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $p) { return }
    $parts = $p.Split(';') | Where-Object { $_ -and ($_.TrimEnd('\') -ne $dir.TrimEnd('\')) }
    [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
}

function Add-ToUserPath($dir) {
    $p = [Environment]::GetEnvironmentVariable('Path', 'User')
    $parts = @()
    if ($p) { $parts = $p.Split(';') | Where-Object { $_ } }
    if ($parts | Where-Object { $_.TrimEnd('\') -eq $dir.TrimEnd('\') }) { return $false }
    [Environment]::SetEnvironmentVariable('Path', (($parts + $dir) -join ';'), 'User')
    return $true
}

if ($Uninstall) {
    Write-Step 'Removendo simple-orchestrator'
    foreach ($t in Get-SkillTargets) {
        $skill = Join-Path $t.Dir $SkillName
        $file = Join-Path $skill 'SKILL.md'
        if ((Test-Path $file) -and (Select-String -Path $file -Pattern 'simple-orchestrator' -Quiet)) {
            Remove-Item $skill -Recurse -Force
            Write-Ok "skill removida de $($t.Dir)"
        }
    }
    Remove-FromUserPath $InstallDir
    if (Test-Path $InstallDir) { Remove-Item $InstallDir -Recurse -Force }
    Write-Ok "binario removido de $InstallDir"
    if ($Purge -and (Test-Path $StateDir)) {
        Remove-Item $StateDir -Recurse -Force
        Write-Ok "estado removido de $StateDir"
    } elseif (Test-Path $StateDir) {
        Write-Host "  Estado das execucoes mantido em $StateDir (use -Purge para remover)."
    }
    return
}

if ($env:OS -ne 'Windows_NT') { throw 'O simple-orchestrator suporta apenas Windows nesta versao.' }

# 1. Locate the binary and the skill: release folder, repository clone, or download.
$work = $null
$root = $PSScriptRoot
$exeSource = $null
$skillSource = $null

if ($root -and (Test-Path (Join-Path $root $ExeName))) {
    Write-Step "Usando os arquivos da pasta $root"
    $exeSource = Join-Path $root $ExeName
    $skillSource = Join-Path $root "skills\$SkillName"
} elseif ($root -and (Test-Path (Join-Path $root 'go.mod')) -and (Test-Path (Join-Path $root 'cmd\simple-orchestrator'))) {
    Write-Step 'Compilando a partir do repositorio'
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        throw 'Go nao encontrado. Instale o Go (https://go.dev/dl/) ou use a release: irm https://raw.githubusercontent.com/jovemcleb/simple-orchestrator/main/install.ps1 | iex'
    }
    $work = Join-Path ([IO.Path]::GetTempPath()) ("simple-orchestrator-" + [guid]::NewGuid())
    New-Item -ItemType Directory $work | Out-Null
    $exeSource = Join-Path $work $ExeName
    Push-Location $root
    try {
        & go build -trimpath -o $exeSource ./cmd/simple-orchestrator
        if ($LASTEXITCODE -ne 0) { throw 'go build falhou.' }
    } finally { Pop-Location }
    $skillSource = Join-Path $root "skills\$SkillName"
} else {
    $tag = if ($Version) { "tags/$Version" } else { 'latest' }
    Write-Step "Baixando a release ($tag) de github.com/$Repo"
    [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
    $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/$tag" -Headers @{ 'User-Agent' = 'simple-orchestrator-installer' }
    $asset = $rel.assets | Where-Object { $_.name -like 'simple-orchestrator-windows-amd64*.zip' } | Select-Object -First 1
    if (-not $asset) { throw "A release $($rel.tag_name) nao tem o pacote simple-orchestrator-windows-amd64.zip." }
    $work = Join-Path ([IO.Path]::GetTempPath()) ("simple-orchestrator-" + [guid]::NewGuid())
    New-Item -ItemType Directory $work | Out-Null
    $zip = Join-Path $work $asset.name
    Invoke-WebRequest $asset.browser_download_url -OutFile $zip -UseBasicParsing
    Expand-Archive $zip -DestinationPath $work -Force
    $exeSource = Join-Path $work $ExeName
    $skillSource = Join-Path $work "skills\$SkillName"
    Write-Ok "release $($rel.tag_name)"
}

if (-not (Test-Path $exeSource)) { throw "Binario nao encontrado em $exeSource." }
if (-not (Test-Path (Join-Path $skillSource 'SKILL.md'))) { throw "Skill nao encontrada em $skillSource." }

try {
    # 2. Binary and PATH.
    Write-Step "Instalando o binario em $InstallDir"
    New-Item -ItemType Directory $InstallDir -Force | Out-Null
    Copy-Item $exeSource (Join-Path $InstallDir $ExeName) -Force
    Write-Ok $ExeName
    if (Add-ToUserPath $InstallDir) { Write-Ok 'pasta adicionada ao PATH do usuario (abra um terminal novo)' } else { Write-Ok 'pasta ja estava no PATH' }

    # 3. Skill in every host found.
    Write-Step "Instalando a skill $SkillName"
    $targets = @(Get-SkillTargets)
    if ($targets.Count -eq 0) {
        Write-Warn2 'Nenhuma pasta do Codex (~/.codex) ou do Antigravity (~/.gemini) encontrada. Instale os hosts e rode este instalador de novo.'
    }
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    foreach ($t in $targets) {
        $dest = Join-Path $t.Dir $SkillName
        if (Test-Path $dest) {
            $same = (Test-Path (Join-Path $dest 'SKILL.md')) -and ((Get-FileHash (Join-Path $dest 'SKILL.md')).Hash -eq (Get-FileHash (Join-Path $skillSource 'SKILL.md')).Hash)
            if ($same) { Write-Ok "$($t.Host): ja atualizada ($dest)"; continue }
            $bk = Join-Path $BackupDir "$($t.Host)-$SkillName-$stamp"
            New-Item -ItemType Directory $BackupDir -Force | Out-Null
            Move-Item $dest $bk
            Write-Host "  copia anterior guardada em $bk"
        }
        New-Item -ItemType Directory $t.Dir -Force | Out-Null
        Copy-Item $skillSource $dest -Recurse -Force
        Write-Ok "$($t.Host): $dest"
    }
} finally {
    if ($work -and (Test-Path $work)) { Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue }
}

# 4. Prerequisites (informative).
Write-Step 'Conferindo pre-requisitos'
$agy = Join-Path $env:LOCALAPPDATA 'agy\bin\agy.EXE'
$checks = @(
    @{ Name = 'Codex CLI (codex)';         Ok = [bool](Get-Command codex -ErrorAction SilentlyContinue); Hint = 'npm install -g @openai/codex  e depois  codex login' },
    @{ Name = 'Antigravity CLI (agy)';     Ok = (Test-Path $agy) -or [bool](Get-Command agy -ErrorAction SilentlyContinue); Hint = 'instale o Antigravity e faca login no agy' },
    @{ Name = 'Python 3 (python)';         Ok = [bool](Get-Command python -ErrorAction SilentlyContinue); Hint = 'necessario para ler os historicos do Codex/Antigravity: https://www.python.org/downloads/' },
    @{ Name = 'Node.js (node)';            Ok = [bool](Get-Command node -ErrorAction SilentlyContinue); Hint = 'necessario para o Codex CLI instalado via npm' },
    @{ Name = 'Windows Terminal (wt)';     Ok = [bool](Get-Command wt -ErrorAction SilentlyContinue); Hint = 'opcional; sem ele as janelas abrem no console classico' }
)
foreach ($c in $checks) {
    if ($c.Ok) { Write-Ok $c.Name } else { Write-Warn2 "$($c.Name) nao encontrado: $($c.Hint)" }
}

Write-Host ''
Write-Host 'Pronto. Proximos passos:' -ForegroundColor Green
Write-Host '  1. Reinicie o Codex e o Antigravity para carregarem a skill.'
Write-Host '  2. No chat, peca por exemplo:'
Write-Host '     "Use a skill local-agent-flow e delegue ao Antigravity: revise o README e liste 3 melhorias (somente leitura)."'
Write-Host '  3. No Codex, aprove a execucao do comando fora do sandbox quando ele pedir.'
Write-Host ''
Write-Host "Ajuda: $InstallDir\$ExeName delegate -h   |   Remover: .\install.ps1 -Uninstall"

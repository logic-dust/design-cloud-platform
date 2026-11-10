# =============================================================================
# Design Cloud Engine - Windows Agent (configless)
# =============================================================================
# One-shot OTP register + Windows service install. No config.toml.
#
#   powershell -ExecutionPolicy Bypass -File .\install-win-agent-configless.ps1 `
#     -PortalUrl "https://mgt01:8443" -Token "<otp>" [-Servers "mgt01,mgt02"]
# =============================================================================

[CmdletBinding()]
param (
    [Parameter(Mandatory = $false, HelpMessage = "Portal HTTP(S) URL, e.g. https://mgt01:8443 (required unless -SkipRegister)")]
    [string]$PortalUrl = "",

    [Parameter(Mandatory = $false, HelpMessage = "OTP from `node issue-token`. Omit with -SkipRegister when certs already exist.")]
    [string]$Token = "",

    [Parameter(Mandatory = $false, HelpMessage = "Cluster servers, comma-separated, no port. Default: host of -PortalUrl")]
    [string]$Servers = "",

    [Parameter(Mandatory = $false, HelpMessage = "Cluster gRPC port")]
    [int]$Port = 52255,

    [Parameter(Mandatory = $false, HelpMessage = "Persistent state root (cluster certs)")]
    [string]$StateDir = "C:\ProgramData\DesignCloudEngine",

    [Parameter(Mandatory = $false, HelpMessage = "Log directory (local disk recommended)")]
    [string]$LogDir = "",

    [Parameter(Mandatory = $false, HelpMessage = "Optional pin: DEBUG|INFO|WARN|ERROR; omit to follow Settings agent.logLevel")]
    [string]$LogLevel = "",

    [Parameter(Mandatory = $false, HelpMessage = "Installation directory for the agent binary")]
    [string]$InstallDir = "C:\Program Files\DesignCloudEngine",

    [Parameter(Mandatory = $false, HelpMessage = "Skip OTP register (replace binary and restart service only)")]
    [switch]$SkipRegister,

    [Parameter(Mandatory = $false, HelpMessage = "Allow plain HTTP portal URL (INSECURE: transmits credentials in plaintext)")]
    [switch]$InsecureHttp
)

$ErrorActionPreference = "Stop"
$ServiceName = "DesignCloudEngine"

if (-not ("DceWinHostname" -as [type])) {
    Add-Type -TypeDefinition @"
using System;
using System.Runtime.InteropServices;
using System.Text;
public static class DceWinHostname {
    [DllImport("kernel32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool GetComputerNameEx(int NameType, StringBuilder lpBuffer, ref uint lpnSize);
}
"@
}

function Get-EngineNodeID {
    $capacity = 256
    $sb = New-Object System.Text.StringBuilder $capacity
    $size = [uint32]$capacity
    $ok = [DceWinHostname]::GetComputerNameEx(5, $sb, [ref]$size)
    if (-not $ok) {
        throw "GetComputerNameEx(ComputerNamePhysicalDnsHostname) failed: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())"
    }
    return $sb.ToString()
}

function Stop-InstalledAgentService {
    param([string]$BinaryPath)
    $svc = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
    if (-not $svc) {
        return
    }
    if ($svc.Status -ne "Stopped") {
        Write-Host "  Stopping $ServiceName so the binary can be replaced..." -ForegroundColor Yellow
        Stop-Service -Name $ServiceName -Force
        $svc.WaitForStatus("Stopped", (New-TimeSpan -Seconds 30))
    }
    if ($BinaryPath -and (Test-Path $BinaryPath)) {
        $proc = Start-Process -FilePath $BinaryPath -ArgumentList @("service", "uninstall") -NoNewWindow -Wait -PassThru
        if ($proc.ExitCode -ne 0) {
            Write-Warning "service uninstall exited $($proc.ExitCode); falling back to sc.exe delete."
            & sc.exe delete $ServiceName | Out-Null
        }
    } else {
        & sc.exe delete $ServiceName | Out-Null
    }
    for ($i = 0; $i -lt 30; $i++) {
        if (-not (Get-Service -Name $ServiceName -ErrorAction SilentlyContinue)) {
            break
        }
        Start-Sleep -Milliseconds 500
    }
}

function Invoke-Engine {
    param([string]$Binary, [string[]]$Arguments, [string]$Failure)
    $proc = Start-Process -FilePath $Binary -ArgumentList $Arguments -NoNewWindow -Wait -PassThru
    if ($proc.ExitCode -ne 0) {
        Write-Error "$Failure (exit $($proc.ExitCode))."
        exit $proc.ExitCode
    }
}

function Set-PrivateAcl {
    param([string]$Path, [bool]$IsDirectory = $true)
    # Use universal SIDs (*S-1-5-18 = SYSTEM, *S-1-5-32-544 = Administrators) so this works on all Windows locales.
    $grant = if ($IsDirectory) { "(OI)(CI)(F)" } else { "(F)" }
    & icacls $Path /inheritance:r /grant:r "*S-1-5-18:$grant" "*S-1-5-32-544:$grant" | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Error "Failed to harden ACL on '$Path' (icacls exit $LASTEXITCODE)."
        exit $LASTEXITCODE
    }
}

$runtimeNodeID = Get-EngineNodeID
$portal = $null
if (-not [string]::IsNullOrWhiteSpace($PortalUrl)) {
    try {
        $portal = [Uri]$PortalUrl
    } catch {
        Write-Error "-PortalUrl must be a full http(s) URL (e.g. https://mgt01:8443)."
        exit 1
    }
    if (-not $portal.IsAbsoluteUri -or ($portal.Scheme -ne "http" -and $portal.Scheme -ne "https") -or [string]::IsNullOrWhiteSpace($portal.Host)) {
        Write-Error "-PortalUrl must be a full http(s) URL (e.g. https://mgt01:8443); got '$PortalUrl'."
        exit 1
    }
    if (-not $SkipRegister -and $portal.Scheme -eq "http" -and -not $InsecureHttp) {
        Write-Warning "SECURITY ERROR: -PortalUrl uses unencrypted 'http://' scheme ($PortalUrl)!"
        Write-Warning "Transmitting OTP tokens and receiving private keys over plain HTTP is vulnerable to MitM interception."
        Write-Warning "Recommended: Use 'https://' with a valid SSL/TLS certificate."
        Write-Error "Insecure plain HTTP rejected. Pass -InsecureHttp to override at your own risk."
        exit 1
    }
} elseif (-not $SkipRegister) {
    Write-Error "-PortalUrl is required unless -SkipRegister is set."
    exit 1
}

if ([string]::IsNullOrWhiteSpace($Servers)) {
    if ($null -ne $portal) {
        $Servers = $portal.Host
    } else {
        Write-Error "-Servers is required when -SkipRegister is set without -PortalUrl."
        exit 1
    }
}
if ($Port -lt 1024 -or $Port -gt 65535) {
    Write-Error "-Port must be in 1024..65535; got $Port."
    exit 1
}
if ([string]::IsNullOrWhiteSpace($LogDir)) {
    $LogDir = Join-Path $StateDir "logs"
}

$currentPrincipal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "Run this script from an elevated PowerShell."
    exit 1
}

Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "  Design Cloud Engine - Windows Agent (configless)" -ForegroundColor Cyan
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "Node ID       : $runtimeNodeID   (os.Hostname(); issue OTP with this exact value)"
if (-not [string]::IsNullOrWhiteSpace($PortalUrl)) {
    Write-Host "Portal URL    : $PortalUrl"
}
Write-Host "Servers       : $Servers"
Write-Host "Cluster Port  : $Port"
Write-Host "Install Dir   : $InstallDir"
Write-Host "Data Dir      : $StateDir"
Write-Host "Log Dir       : $LogDir"
Write-Host ""

Write-Host "[1/4] Preparing directories..." -ForegroundColor Yellow
foreach ($d in @($InstallDir, $StateDir, (Join-Path $StateDir "certs\cluster"), $LogDir)) {
    if (-not (Test-Path $d)) {
        New-Item -ItemType Directory -Path $d -Force | Out-Null
    }
}

Write-Host "[2/4] Installing binary..." -ForegroundColor Yellow
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$sourceBinary = $null
# Prefer make win-agent output (dc-engine.exe); keep legacy names as source fallbacks.
foreach ($name in @("dc-engine.exe", "design-cloud-engine.exe", "engine.exe")) {
    $candidate = Join-Path $scriptDir $name
    if (Test-Path $candidate) {
        $sourceBinary = $candidate
        break
    }
}
$targetBinary = Join-Path $InstallDir "dc-engine.exe"
$legacyBinary = Join-Path $InstallDir "design-cloud-engine.exe"
Stop-InstalledAgentService -BinaryPath $targetBinary
if (Test-Path $legacyBinary) {
    Stop-InstalledAgentService -BinaryPath $legacyBinary
}
if ($sourceBinary) {
    Copy-Item -Path $sourceBinary -Destination $targetBinary -Force
    Write-Host "  Copied $(Split-Path -Leaf $sourceBinary) to $targetBinary" -ForegroundColor Green
} elseif (-not (Test-Path $targetBinary)) {
    Write-Error "dc-engine.exe not found next to this script or in '$InstallDir'."
    exit 1
}

$clusterCertDir = Join-Path $StateDir "certs\cluster"
$caPath = Join-Path $clusterCertDir "ca.crt"
Set-PrivateAcl -Path $StateDir -IsDirectory $true
Set-PrivateAcl -Path $clusterCertDir -IsDirectory $true
if ($SkipRegister) {
    if (-not (Test-Path $caPath)) {
        Write-Error "-SkipRegister requires existing certificates under $clusterCertDir. Pass -Token for first install."
        exit 1
    }
    foreach ($certFile in @("ca.crt", "node.crt", "node.key")) {
        $p = Join-Path $clusterCertDir $certFile
        if (Test-Path $p) {
            Set-PrivateAcl -Path $p -IsDirectory $false
        }
    }
    Write-Host "[3/4] Skipping OTP register (certificates present; ACLs hardened)." -ForegroundColor Yellow
} else {
    if ([string]::IsNullOrWhiteSpace($Token)) {
        Write-Error "-Token is required unless -SkipRegister is set."
        exit 1
    }
    Write-Host "[3/4] Registering node and fetching certificates..." -ForegroundColor Yellow
    $registerArgs = @(
        "node", "register",
        "--portal-url", $PortalUrl,
        "--token", $Token,
        "--state-dir", $StateDir
    )
    if ($InsecureHttp) {
        $registerArgs += "--insecure-http"
    }
    Invoke-Engine -Binary $targetBinary -Arguments $registerArgs -Failure "Certificate bootstrap failed. Check Portal URL, token, and that the OTP hostname equals '$runtimeNodeID'"
    Write-Host "  Certificates written under $StateDir\certs\cluster" -ForegroundColor Green
}

if ($LogLevel -ne "" -and $LogLevel -notin @("DEBUG", "INFO", "WARN", "ERROR")) {
    throw "LogLevel must be DEBUG|INFO|WARN|ERROR (or empty to follow Settings); got '$LogLevel'"
}

Write-Host "[4/4] Registering and starting Windows service..." -ForegroundColor Yellow
$serviceArgs = @(
    "service", "install",
    "--servers", $Servers,
    "--port", "$Port",
    "--state-dir", $StateDir,
    "--log-dir", $LogDir
)
if ($LogLevel -ne "") {
    $serviceArgs += @("--log-level", $LogLevel)
}
Invoke-Engine -Binary $targetBinary -Arguments $serviceArgs -Failure "Service installation failed"

$startProc = Start-Process -FilePath $targetBinary -ArgumentList @("service", "start") -NoNewWindow -Wait -PassThru
if ($startProc.ExitCode -ne 0) {
    Write-Warning "service start returned $($startProc.ExitCode). Checking service status..."
}

Start-Sleep -Seconds 2
$svcStatus = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($svcStatus -and $svcStatus.Status -eq "Running") {
    Write-Host ""
    Write-Host "============================================================" -ForegroundColor Green
    Write-Host "  SUCCESS! Configless agent is RUNNING as a service." -ForegroundColor Green
    Write-Host "============================================================" -ForegroundColor Green
    Write-Host "Service Name  : $ServiceName"
    Write-Host "Service Status: $($svcStatus.Status)"
    Write-Host "Start command : --servers $Servers --port $Port --state-dir $StateDir"
} else {
    Write-Warning "Service is not Running (Current: $($svcStatus.Status)). Check Event Viewer or $LogDir."
}

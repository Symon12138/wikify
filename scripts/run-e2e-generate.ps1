# Real LLM generate on the sample fixture. Run in PowerShell.
$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
Set-Location $Root

$Fix = if ($args[0]) { $args[0] } else { Join-Path $Root "testdata\e2e-sample" }
$Log = if ($args[1]) { $args[1] } else { Join-Path $Root "testdata\e2e-generate.log" }
$Bin = Join-Path $Root "wikify.exe"

if (-not (Test-Path $Bin)) {
  Write-Host "building wikify.exe ..."
  go build -ldflags "-s -w -X main.appVersion=dev" -o wikify.exe .
}

Write-Host "binary: $Bin"
Write-Host "target: $Fix"
Write-Host "log:    $Log"
& $Bin config

Write-Host "`n>>> generate (max-pages=20, workers=3)"
& $Bin generate --dir $Fix -y --draft clear --max-pages 20 --lang Chinese --workers 3 --retries 2 --verbose-catalog 2>&1 | Tee-Object -FilePath $Log

Write-Host "`n>>> polish"
& $Bin polish --dir $Fix --export-lang zh

Write-Host "`n>>> artifacts"
Get-ChildItem (Join-Path $Fix ".wikify\meta") -ErrorAction SilentlyContinue
(Get-ChildItem (Join-Path $Fix ".wikify\content") -Recurse -Filter *.md -ErrorAction SilentlyContinue).Count
Write-Host "browse: $Bin browse --dir $Fix"

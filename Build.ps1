param(
  [switch]$SkipChecks,
  [string]$Version = '',
  [string]$OutputPath = 'dist/HongguoDesktop.exe'
)
$ErrorActionPreference = 'Stop'
$projectRoot = $PSScriptRoot
$localGo = Join-Path (Split-Path $projectRoot -Parent) '.tools\go\bin\go.exe'
if (Test-Path -LiteralPath $localGo) { $goBinary = $localGo }
else { $goBinary = (Get-Command go -ErrorAction Stop).Source }
$nodeBinary = (Get-Command node -ErrorAction Stop).Source
Push-Location $projectRoot
try {
  $config = Get-Content -LiteralPath (Join-Path $projectRoot 'wails.json') -Raw | ConvertFrom-Json
  if (-not $Version) { $Version = $config.info.productVersion }
  if ($Version -notmatch '^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$') { throw "版本号无效：$Version" }
  $buildTags = 'desktop,production,nodynamic,wv2runtime.error'
  & $goBinary -C tools/appresources run . -root $projectRoot -arch amd64 -version $Version
  if ($LASTEXITCODE -ne 0) { throw '应用图标与 Windows 资源生成失败' }
  & $nodeBinary scripts/build-frontend.mjs
  if ($LASTEXITCODE -ne 0) { throw '前端资源构建失败' }
  if (-not $SkipChecks) {
    & $nodeBinary --test scripts/compat.test.mjs
    if ($LASTEXITCODE -ne 0) { throw '界面适配测试失败' }
    & $goBinary test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go 测试失败' }
    & $goBinary vet ./...
    if ($LASTEXITCODE -ne 0) { throw '静态检查失败' }
  }
  $outputDirectory = Split-Path -Path $OutputPath -Parent
  if ($outputDirectory) { New-Item -ItemType Directory -Force -Path $outputDirectory | Out-Null }
  $linkerFlags = "-H=windowsgui -s -w -X hongguo-desktop/internal/app.Version=$Version"
  & $goBinary build -tags $buildTags -trimpath -ldflags $linkerFlags -o $OutputPath .
  if ($LASTEXITCODE -ne 0) { throw 'Windows 构建失败；如程序正在运行，请先退出' }
  Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256
  Write-Host "构建成功：$OutputPath"
} finally { Pop-Location }

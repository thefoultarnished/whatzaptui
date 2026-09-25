param(
    [ValidateSet("build", "test", "run", "clean")]
    [string]$Command = "build"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root "dist"

Push-Location $Root
try {
    switch ($Command) {
        "build" {
            New-Item -ItemType Directory -Force -Path $Dist | Out-Null
            $oldBackend = Join-Path $Dist "backend.exe"
            if (Test-Path $oldBackend) {
                Remove-Item -LiteralPath $oldBackend -Force
            }
            go build -o (Join-Path $Dist "whatzap.exe") ./cmd/whatzap
        }
        "test" {
            go test ./...
        }
        "run" {
            go run ./cmd/whatzap
        }
        "clean" {
            if (Test-Path $Dist) {
                Remove-Item -LiteralPath $Dist -Recurse -Force
            }
        }
    }
}
finally {
    Pop-Location
}
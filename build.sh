#!/usr/bin/env bash
# Builds starmusician-gui.exe (the primary build - see PROJECT_CONTEXT.md;
# the CLI exists only to exercise the engine, not for end users) and embeds
# the elevation manifest via rcedit.
#
# Usage: ./build.sh        builds the GUI only
#        ./build.sh all    also builds the CLI
set -euo pipefail

echo "Building starmusician-gui.exe..."
go build -ldflags "-H windowsgui" -o starmusician-gui.exe ./cmd/starmusician-gui
rcedit starmusician-gui.exe --application-manifest cmd/starmusician-gui/app.manifest

if [ "${1:-}" = "all" ]; then
    echo "Building starmusician-cli.exe..."
    go build -o starmusician-cli.exe ./cmd/starmusician-cli
    rcedit starmusician-cli.exe --application-manifest cmd/starmusician-cli/app.manifest
fi

echo "Done."

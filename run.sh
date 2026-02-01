#!/bin/sh

# Reign development run script
# This script sets up the environment and runs reign in development mode

# Configuration
export REIGN_LOG_LEVEL="debug"

# Create data directory if needed
mkdir -p ./data

# Run with local config
exec go run . -config config.json "$@"

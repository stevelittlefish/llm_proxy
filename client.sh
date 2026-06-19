#!/usr/bin/env sh
set -eu

go run ./cmd/chatclient -config config.toml "$@"
